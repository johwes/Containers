package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Global State
var (
	isHealthy      = true
	isReady        = true
	isTerminating  = false
	readyTime      time.Time
	data           WorkloadInfo
	mu             sync.RWMutex
	prevCPUUsage   int64
	prevCPUTime    time.Time
	currentCPUPct  float64
)

type PodDetail struct {
	Name string
	Node string
}

type WorkloadInfo struct {
	Kind      string
	KindName  string
	RSName    string
	Pods      []PodDetail
	LastError error
}

func main() {
	podName := os.Getenv("POD_NAME")
	namespace := os.Getenv("POD_NAMESPACE")
	retryStr := os.Getenv("API_RETRY")
	retry, _ := strconv.Atoi(retryStr)
	if retry <= 0 { retry = 2 }

	// --- SIGNAL HANDLING (SIGTERM) ---
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-sigChan
		log.Printf("STDOUT: RECEIVED %v. Initiating 15s Graceful Shutdown...", sig)
		mu.Lock()
		isTerminating = true
		isReady = false
		mu.Unlock()
		time.Sleep(15 * time.Second)
		os.Exit(0)
	}()

	// --- BACKGROUND MONITORING ---
	go monitor(podName, namespace, time.Duration(retry)*time.Second)
	
	// --- BACKGROUND CPU CALCULATOR ---
	go func() {
		for {
			usage, _ := readCgroupCPU()
			now := time.Now()
			
			mu.Lock()
			if !prevCPUTime.IsZero() {
				diffUsage := usage - prevCPUUsage
				diffTime := now.Sub(prevCPUTime).Microseconds()
				if diffTime > 0 {
					currentCPUPct = (float64(diffUsage) / float64(diffTime)) * 100
				}
			}
			prevCPUUsage = usage
			prevCPUTime = now
			mu.Unlock()
			time.Sleep(1 * time.Second)
		}
	}()

	// --- PROBES & ACTIONS ---
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock(); defer mu.RUnlock()
		if isHealthy { w.WriteHeader(http.StatusOK) } else { w.WriteHeader(500) }
	})

	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if !isReady && !isTerminating && time.Now().After(readyTime) { isReady = true }
		mu.Unlock()
		mu.RLock(); defer mu.RUnlock()
		if isReady { w.WriteHeader(http.StatusOK) } else { w.WriteHeader(503) }
	})

	http.HandleFunc("/spin-cpu", func(w http.ResponseWriter, r *http.Request) {
	    log.Println("STDOUT: Stressing ALL cores for 10 seconds...")
	    
	    // Determine how many workers to spawn. 
	    // runtime.NumCPU() returns the number of logical CPUs on the Node.
	    // Spawning this many workers ensures we hit the K8s limit regardless of what it is.
	    numWorkers := runtime.NumCPU()

	    for i := 0; i < numWorkers; i++ {
		go func(id int) {
		    done := time.After(15 * time.Second)
		    for {
			select {
			case <-done:
			    return
			default:
			    // Perform a "useless" calculation to keep the CPU busy
			    _ = id * id 
			}
		    }
		}(i)
	    }
	    http.Redirect(w, r, "/", 303)
	})
	http.HandleFunc("/simulate-load", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock(); isReady = false; readyTime = time.Now().Add(60 * time.Second); mu.Unlock()
		http.Redirect(w, r, "/", 303)
	})

	http.HandleFunc("/break-life", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock(); isHealthy = false; mu.Unlock()
		http.Redirect(w, r, "/", 303)
	})

	// --- MAIN UI ---
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock(); defer mu.RUnlock()
		
		// FIX: Use blank identifier for unused 'usage' variable
		_, throttled := readCgroupCPU()
		limit := readCPULimit()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!DOCTYPE html><html><head><meta charset='UTF-8'><style>")
		fmt.Fprint(w, "body{font-family:sans-serif;background:#f4f7f9;padding:20px;color:#2d3748;}")
		fmt.Fprint(w, ".card{max-width:950px;margin:auto;background:white;padding:35px;border-radius:12px;box-shadow:0 4px 15px rgba(0,0,0,0.1);position:relative;}")
		fmt.Fprint(w, ".metric-grid{display:grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap:15px; margin:20px 0;}")
		fmt.Fprint(w, ".metric-card{background:#f8fafc; padding:15px; border-radius:8px; border:1px solid #e2e8f0;}")
		fmt.Fprint(w, "table{width:100%; border-collapse:collapse; margin-top:20px;}")
		fmt.Fprint(w, "td, th{padding:12px; border-bottom:1px solid #edf2f7; text-align:left; font-family:monospace;}")
		fmt.Fprint(w, ".current{background:#ebf8ff; font-weight:bold; color:#3182ce;}")
		fmt.Fprint(w, ".btn{padding:10px 15px; border-radius:6px; color:white; text-decoration:none; font-weight:bold; font-size:0.8rem; margin-right:5px; transition:0.2s;}")
		fmt.Fprint(w, ".btn-blue{background:#3182ce;} .btn-orange{background:#ed8936;} .btn-red{background:#e53e3e;}")
		fmt.Fprint(w, ".overlay{position:absolute; inset:0; background:rgba(255,255,255,0.9); z-index:10; display:flex; flex-direction:column; justify-content:center; align-items:center; border-radius:12px;}")
		fmt.Fprint(w, "</style></head><body><div class='card'>")

		if isTerminating {
			fmt.Fprint(w, "<div class='overlay'><h1 style='color:#c53030;'>⚠️ SIGTERM RECEIVED</h1><p>Gracefully exiting in 15s... check STDOUT logs.</p></div>")
		}

		fmt.Fprint(w, "<h1 style='color:#2b6cb0; margin:0;'>K8s Node-Aware Dashboard</h1>")

		fmt.Fprint(w, "<div class='metric-grid'>")
		fmt.Fprintf(w, "<div class='metric-card'><b>CPU Usage</b><br><span style='font-size:1.5rem;'>%.1f%%</span></div>", currentCPUPct)
		fmt.Fprintf(w, "<div class='metric-card'><b>CPU Limit (Quota)</b><br><code>%s</code></div>", limit)
		fmt.Fprintf(w, "<div class='metric-card'><b>Mem Usage</b><br><span style='font-size:1.5rem;'>%.1f MB</span></div>", float64(m.Alloc)/1024/1024)
		
		throttleColor := "#718096"
		if throttled > 100 { throttleColor = "#e53e3e" }
		fmt.Fprintf(w, "<div class='metric-card' style='color:%s;'><b>Total Throttled</b><br>%.2f s</div>", throttleColor, float64(throttled)/1000000)
		fmt.Fprint(w, "</div>")

		if !isReady && !isTerminating {
			fmt.Fprintf(w, "<div style='background:#fffaf0; padding:10px; border:1px solid #feebc8; border-radius:6px; margin-bottom:15px;'>⏳ Recovering Readiness: %.0fs</div>", time.Until(readyTime).Seconds())
		}

		fmt.Fprintf(w, "<p><b>Workload:</b> %s (<code>%s</code>)</p>", data.Kind, data.KindName)
		fmt.Fprint(w, "<table><thead><tr><th></th><th>Pod Name</th><th>Node</th></tr></thead><tbody>")
		for _, p := range data.Pods {
			rowCls := ""; color := "#cbd5e0"; if p.Name == podName { rowCls = "current"; color = "#3182ce" }
			icon := fmt.Sprintf(`<svg viewBox="0 0 24 24" width="18" height="18" fill="%s" style="vertical-align:middle;margin-right:8px;"><circle cx="12" cy="12" r="10"/></svg>`, color)
			fmt.Fprintf(w, "<tr class='%s'><td>%s</td><td>%s</td><td>%s</td></tr>", rowCls, icon, p.Name, p.Node)
		}
		fmt.Fprint(w, "</tbody></table>")

		fmt.Fprint(w, "<div style='margin-top:25px;'>")
		fmt.Fprint(w, "<a href='/spin-cpu' class='btn btn-blue'>Spin CPU (Stress)</a>")
		fmt.Fprint(w, "<a href='/simulate-load' class='btn btn-orange'>Busy (60s Ready)</a>")
		fmt.Fprint(w, "<a href='/break-life' class='btn btn-red'>Break Health (Life)</a>")
		fmt.Fprint(w, "</div>")
		fmt.Fprint(w, "</div></body></html>")
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func readCgroupCPU() (usage int64, throttled int64) {
	f, err := os.Open("/sys/fs/cgroup/cpu.stat")
	if err != nil { return 0, 0 }
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 { continue }
		if fields[0] == "usage_usec" { usage, _ = strconv.ParseInt(fields[1], 10, 64) }
		if fields[0] == "throttled_usec" { throttled, _ = strconv.ParseInt(fields[1], 10, 64) }
	}
	return
}

func readCPULimit() string {
	data, _ := os.ReadFile("/sys/fs/cgroup/cpu.max")
	parts := strings.Fields(string(data))
	if len(parts) >= 1 && parts[0] == "max" { return "Unlimited" }
	if len(parts) >= 2 {
		quota, _ := strconv.ParseFloat(parts[0], 64)
		period, _ := strconv.ParseFloat(parts[1], 64)
		return fmt.Sprintf("%.1f Cores", quota/period)
	}
	return "Unknown"
}

func monitor(podName, ns string, interval time.Duration) {
	for {
		info := fetch(podName, ns)
		mu.Lock(); data = info; mu.Unlock()
		time.Sleep(interval)
	}
}

func fetch(podName, ns string) WorkloadInfo {
	cfg, _ := rest.InClusterConfig()
	client, _ := kubernetes.NewForConfig(cfg)
	p, err := client.CoreV1().Pods(ns).Get(context.Background(), podName, metav1.GetOptions{})
	if err != nil { return WorkloadInfo{LastError: err} }

	var selector string
	var info WorkloadInfo
	for _, o := range p.OwnerReferences {
		if o.Name == "" { continue }
		switch o.Kind {
		case "StatefulSet":
			ss, _ := client.AppsV1().StatefulSets(ns).Get(context.Background(), o.Name, metav1.GetOptions{})
			selector = metav1.FormatLabelSelector(ss.Spec.Selector)
			info = WorkloadInfo{Kind: "StatefulSet", KindName: o.Name}
		case "ReplicaSet":
			rs, _ := client.AppsV1().ReplicaSets(ns).Get(context.Background(), o.Name, metav1.GetOptions{})
			for _, ro := range rs.OwnerReferences {
				if ro.Kind == "Deployment" {
					info = WorkloadInfo{Kind: "Deployment", KindName: ro.Name, RSName: rs.Name}
					selector = metav1.FormatLabelSelector(rs.Spec.Selector)
				}
			}
		case "DaemonSet":
			ds, _ := client.AppsV1().DaemonSets(ns).Get(context.Background(), o.Name, metav1.GetOptions{})
			selector = metav1.FormatLabelSelector(ds.Spec.Selector)
			info = WorkloadInfo{Kind: "DaemonSet", KindName: o.Name}
		}
	}
	if selector != "" {
		podList, _ := client.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{LabelSelector: selector})
		for _, item := range podList.Items {
			info.Pods = append(info.Pods, PodDetail{Name: item.Name, Node: item.Spec.NodeName})
		}
	}
	return info
}
