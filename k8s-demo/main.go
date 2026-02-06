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
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
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
	mu             sync.RWMutex
	currentCPUPct  float64
	prevCPUUsage   int64
	prevCPUTime    time.Time
	fullData       DeploymentData
)

type PodInfo struct {
	Name     string
	Ready    string
	Status   string
	Restarts int32
	Node     string
	IsActive bool
}

type RSInfo struct {
	Name    string
	Desired int32
	Pods    []PodInfo
}

type DeploymentData struct {
	Name        string
	Kind        string
	Replicas    int32
	Available   int32
	ReplicaSets []RSInfo
}

func main() {
	podName := os.Getenv("POD_NAME")
	namespace := os.Getenv("POD_NAMESPACE")

	// --- SIGNAL HANDLING (SIGTERM) ---
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigChan
		log.Printf("STDOUT: %v received. Initiating Graceful Shutdown (15s)...", sig)
		mu.Lock()
		isTerminating = true
		isReady = false 
		mu.Unlock()
		time.Sleep(15 * time.Second)
		os.Exit(0)
	}()

	// --- BACKGROUND TASKS ---
	go monitorTree(podName, namespace)
	go calculateCPUUsage()

	// --- PROBE HANDLERS ---
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock(); defer mu.RUnlock()
		if isHealthy { w.WriteHeader(200) } else { w.WriteHeader(500) }
	})

	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if !isReady && !isTerminating && time.Now().After(readyTime) { isReady = true }
		mu.Unlock()
		mu.RLock(); defer mu.RUnlock()
		if isReady { w.WriteHeader(200) } else { w.WriteHeader(503) }
	})

	// --- ACTION HANDLERS ---
	http.HandleFunc("/spin-cpu", func(w http.ResponseWriter, r *http.Request) {
		log.Println("STDOUT: Stressing all cores for 10 seconds...")
		numWorkers := runtime.NumCPU()
		for i := 0; i < numWorkers; i++ {
			go func() {
				done := time.After(10 * time.Second)
				for { select { case <-done: return; default: } }
			}()
		}
		http.Redirect(w, r, "/", 303)
	})

	http.HandleFunc("/simulate-load", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock(); isReady = false; readyTime = time.Now().Add(60 * time.Second); mu.Unlock()
		http.Redirect(w, r, "/", 303)
	})

	http.HandleFunc("/break-life", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock(); isHealthy = false; mu.Unlock()
		log.Println("STDOUT: Liveness probe failed manually.")
		http.Redirect(w, r, "/", 303)
	})

	// --- MAIN UI RENDERER ---
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock(); defer mu.RUnlock()
		_, throttled := readCgroupCPU()
		limit := readCPULimit()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!DOCTYPE html><html><head><style>")
		fmt.Fprint(w, "body{font-family:sans-serif; background:#f4f7f9; padding:20px; color:#2d3748;}")
		fmt.Fprint(w, ".card{max-width:1000px; margin:auto; background:white; padding:35px; border-radius:12px; box-shadow:0 10px 30px rgba(0,0,0,0.05); position:relative;}")
		fmt.Fprint(w, ".header-box{background:#2b6cb0; color:white; padding:20px; border-radius:10px; margin-bottom:25px;}")
		fmt.Fprint(w, ".progress-bg{background:rgba(255,255,255,0.2); height:10px; border-radius:5px; margin-top:10px; overflow:hidden;}")
		fmt.Fprint(w, ".progress-fill{background:#48bb78; height:100%; transition: width 0.5s;}")
		fmt.Fprint(w, ".m-card{background:#edf2f7; padding:12px; border-radius:8px; font-size:0.85rem; border:1px solid #e2e8f0; flex:1;}")
		fmt.Fprint(w, ".tree-node{margin-left:15px; border-left:2px solid #cbd5e0; padding-left:20px; margin-top:20px;}")
		fmt.Fprint(w, ".rs-label{background:#ebf8ff; color:#2c5282; padding:6px 12px; border-radius:4px; font-weight:bold; font-size:0.9rem; margin-bottom:10px; display:inline-block;}")
		fmt.Fprint(w, "table{width:100%; border-collapse:collapse; margin-top:5px;}")
		fmt.Fprint(w, "th{text-align:left; color:#718096; font-size:0.7rem; text-transform:uppercase; padding:10px; border-bottom:1px solid #edf2f7;}")
		fmt.Fprint(w, "td{padding:10px; border-bottom:1px solid #edf2f7; font-family:monospace; font-size:0.85rem;}")
		fmt.Fprint(w, ".active-row{background:#fffaf0; border-left:4px solid #ed8936;}")
		fmt.Fprint(w, ".status-term{color:#e53e3e; font-weight:bold;}")
		fmt.Fprint(w, ".btn{padding:10px 18px; border-radius:6px; color:white; text-decoration:none; font-weight:bold; font-size:0.8rem; display:inline-block; border:none; cursor:pointer;}")
		fmt.Fprint(w, ".overlay{position:absolute; inset:0; background:rgba(255,255,255,0.95); z-index:200; display:flex; flex-direction:column; justify-content:center; align-items:center; border-radius:12px; text-align:center;}")
		fmt.Fprint(w, ".live-indicator{display:inline-block; font-size:0.8rem; color:#48bb78; font-weight:bold;}")
		fmt.Fprint(w, ".dot{height:8px; width:8px; background-color:#48bb78; border-radius:50%; display:inline-block; margin-right:5px; animation: blink 1s infinite;}")
		fmt.Fprint(w, ".banner-life{background:#fff5f5; border:1px solid #feb2b2; padding:15px; border-radius:8px; margin-bottom:20px; color:#c53030;}")
		fmt.Fprint(w, "@keyframes blink { 0% { opacity:1; } 50% { opacity:0; } 100% { opacity:1; } }")
		fmt.Fprint(w, "</style>")
		
		fmt.Fprint(w, `<script>
			let refreshing = true;
			function checkRefresh() { if (refreshing) { location.reload(); } }
			setTimeout(checkRefresh, 1000);
			function togglePause() {
				refreshing = !refreshing;
				document.getElementById("btn-pause").innerText = refreshing ? "Pause" : "Resume";
				document.getElementById("indicator").style.visibility = refreshing ? "visible" : "hidden";
			}
		</script>`)
		fmt.Fprint(w, "</head><body><div class='card'>")

		if isTerminating {
			fmt.Fprint(w, "<div class='overlay'><h1>🛑 SIGTERM RECEIVED</h1><p>Pod is <b>Terminating</b>.<br>Removing from LoadBalancer. Finishing work (15s)...</p></div>")
		}

		// Header
		fmt.Fprint(w, "<div style='display:flex; justify-content:space-between; align-items:center; margin-bottom:20px;'>")
		fmt.Fprint(w, "<h1 style='color:#2b6cb0; margin:0;'>K8s Resilience Dashboard</h1>")
		fmt.Fprint(w, "<div><span id='indicator' class='live-indicator'><span class='dot'></span>LIVE</span>")
		fmt.Fprint(w, "<button id='btn-pause' onclick='togglePause()' style='margin-left:15px; cursor:pointer; font-size:0.7rem; border:1px solid #cbd5e0; background:white; padding:4px 8px; border-radius:4px;'>Pause</button></div></div>")

		pct := 0.0
		if fullData.Replicas > 0 { pct = (float64(fullData.Available) / float64(fullData.Replicas)) * 100 }
		fmt.Fprint(w, "<div class='header-box'>")
		fmt.Fprintf(w, "<div style='display:flex; justify-content:space-between;'><b>%s: %s</b> <span>%d/%d Ready</span></div>", fullData.Kind, fullData.Name, fullData.Available, fullData.Replicas)
		fmt.Fprintf(w, "<div class='progress-bg'><div class='progress-fill' style='width:%.0f%%;'></div></div>", pct)
		fmt.Fprint(w, "</div>")

		// Metrics
		fmt.Fprint(w, "<div style='display:flex; gap:10px; margin-bottom:20px;'>")
		fmt.Fprintf(w, "<div class='m-card'><b>CPU:</b> %.1f%%<br><small>Limit: %s</small></div>", currentCPUPct, limit)
		fmt.Fprintf(w, "<div class='m-card'><b>Memory:</b> %.1f MB</div>", float64(m.Alloc)/1024/1024)
		fmt.Fprintf(w, "<div class='m-card'><b>Throttled:</b> %.2fs</div>", float64(throttled)/1000000)
		fmt.Fprint(w, "</div>")

		// Warnings
		if !isHealthy {
			fmt.Fprint(w, "<div class='banner-life'><b>LIVENESS FAILED:</b> K8s will RESTART this container shortly.</div>")
		}
		if !isReady && !isTerminating && isHealthy {
			fmt.Fprintf(w, "<div style='background:#fffaf0; padding:12px; border:1px solid #feebc8; border-radius:6px; margin-bottom:20px; color:#c05621;'>⏳ <b>READINESS FAILED:</b> Recovering in %.0fs</div>", time.Until(readyTime).Seconds())
		}

		// Tree
		for _, rs := range fullData.ReplicaSets {
			fmt.Fprintf(w, "<div class='tree-node'><div class='rs-label'>%s</div>", rs.Name)
			fmt.Fprint(w, "<table><thead><tr><th>READY</th><th>STATUS</th><th>POD NAME</th><th>RESTARTS</th><th>NODE</th></tr></thead><tbody>")
			for _, p := range rs.Pods {
				rowCls := ""; if p.IsActive { rowCls = "active-row" }
				statCls := ""; if p.Status == "Terminating" { statCls = "status-term" }
				fmt.Fprintf(w, "<tr class='%s'><td>%s</td><td class='%s'>%s</td><td>%s</td><td>%d</td><td>%s</td></tr>", rowCls, p.Ready, statCls, p.Status, p.Name, p.Restarts, p.Node)
			}
			fmt.Fprint(w, "</tbody></table></div>")
		}

		// Controls
		fmt.Fprint(w, "<div style='margin-top:25px; display:flex; gap:10px;'>")
		fmt.Fprint(w, "<a href='/spin-cpu' class='btn' style='background:#3182ce;'>Stress CPU</a> ")
		fmt.Fprint(w, "<a href='/simulate-load' class='btn' style='background:#ed8936;'>Simulate Load</a> ")
		fmt.Fprint(w, "<a href='/break-life' class='btn' style='background:#e53e3e;'>Break Health</a></div>")
		fmt.Fprint(w, "</div></body></html>")
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func buildPodInfo(pItem corev1.Pod, currentPod string) PodInfo {
	readyCount := 0
	for _, cs := range pItem.Status.ContainerStatuses { if cs.Ready { readyCount++ } }
	readyStr := fmt.Sprintf("%d/%d", readyCount, len(pItem.Spec.Containers))
	status := string(pItem.Status.Phase)
	if pItem.DeletionTimestamp != nil { status = "Terminating" }
	restarts := int32(0)
	if len(pItem.Status.ContainerStatuses) > 0 { restarts = pItem.Status.ContainerStatuses[0].RestartCount }
	return PodInfo{Name: pItem.Name, Ready: readyStr, Status: status, Restarts: restarts, Node: pItem.Spec.NodeName, IsActive: pItem.Name == currentPod}
}

func monitorTree(currentPod, ns string) {
	for {
		cfg, _ := rest.InClusterConfig()
		client, _ := kubernetes.NewForConfig(cfg)
		pod, err := client.CoreV1().Pods(ns).Get(context.Background(), currentPod, metav1.GetOptions{})
		if err != nil { time.Sleep(2 * time.Second); continue }

		var d DeploymentData
		var labelSelector string
		for _, o := range pod.OwnerReferences {
			if o.Kind == "ReplicaSet" {
				rs, _ := client.AppsV1().ReplicaSets(ns).Get(context.Background(), o.Name, metav1.GetOptions{})
				for _, ro := range rs.OwnerReferences {
					if ro.Kind == "Deployment" {
						dep, _ := client.AppsV1().Deployments(ns).Get(context.Background(), ro.Name, metav1.GetOptions{})
						d.Name = dep.Name; d.Kind = "Deployment"; d.Replicas = *dep.Spec.Replicas; d.Available = dep.Status.AvailableReplicas
						labelSelector = metav1.FormatLabelSelector(dep.Spec.Selector)
					}
				}
			} else if o.Kind == "StatefulSet" {
				ss, _ := client.AppsV1().StatefulSets(ns).Get(context.Background(), o.Name, metav1.GetOptions{})
				d.Name = ss.Name; d.Kind = "StatefulSet"; d.Replicas = *ss.Spec.Replicas; d.Available = ss.Status.ReadyReplicas
				labelSelector = metav1.FormatLabelSelector(ss.Spec.Selector)
			}
		}

		if labelSelector != "" {
			if d.Kind == "Deployment" {
				allRS, _ := client.AppsV1().ReplicaSets(ns).List(context.Background(), metav1.ListOptions{LabelSelector: labelSelector})
				d.ReplicaSets = nil
				for _, rsItem := range allRS.Items {
					owned := false
					for _, rso := range rsItem.OwnerReferences { if rso.Name == d.Name { owned = true; break } }
					if !owned { continue }
					thisRS := RSInfo{Name: rsItem.Name, Desired: *rsItem.Spec.Replicas}
					pSel := metav1.FormatLabelSelector(rsItem.Spec.Selector)
					allPods, _ := client.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{LabelSelector: pSel})
					for _, pItem := range allPods.Items { thisRS.Pods = append(thisRS.Pods, buildPodInfo(pItem, currentPod)) }
					d.ReplicaSets = append(d.ReplicaSets, thisRS)
				}
				sort.Slice(d.ReplicaSets, func(i, j int) bool { return d.ReplicaSets[i].Desired > d.ReplicaSets[j].Desired })
			} else if d.Kind == "StatefulSet" {
				thisRS := RSInfo{Name: "StatefulSet: " + d.Name, Desired: d.Replicas}
				allPods, _ := client.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{LabelSelector: labelSelector})
				for _, pItem := range allPods.Items { thisRS.Pods = append(thisRS.Pods, buildPodInfo(pItem, currentPod)) }
				sort.Slice(thisRS.Pods, func(i, j int) bool { return thisRS.Pods[i].Name < thisRS.Pods[j].Name })
				d.ReplicaSets = []RSInfo{thisRS}
			}
		}
		mu.Lock(); fullData = d; mu.Unlock(); time.Sleep(2 * time.Second)
	}
}

func calculateCPUUsage() {
	for {
		u, _ := readCgroupCPU(); n := time.Now()
		mu.Lock()
		if !prevCPUTime.IsZero() {
			dt := n.Sub(prevCPUTime).Microseconds()
			if dt > 0 { currentCPUPct = (float64(u-prevCPUUsage) / float64(dt)) * 100 }
		}
		prevCPUUsage = u; prevCPUTime = n; mu.Unlock()
		time.Sleep(time.Second)
	}
}

func readCgroupCPU() (int64, int64) {
	f, err := os.Open("/sys/fs/cgroup/cpu.stat"); if err != nil { return 0,0 }; defer f.Close()
	var u, t int64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 2 { continue }
		if fl[0] == "usage_usec" { u, _ = strconv.ParseInt(fl[1], 10, 64) }
		if fl[0] == "throttled_usec" { t, _ = strconv.ParseInt(fl[1], 10, 64) }
	}
	return u, t
}

func readCPULimit() string {
	d, _ := os.ReadFile("/sys/fs/cgroup/cpu.max")
	fl := strings.Fields(string(d))
	if len(fl) > 0 && fl[0] == "max" { return "Unlimited" }
	if len(fl) >= 2 {
		q, _ := strconv.ParseFloat(fl[0], 64); p, _ := strconv.ParseFloat(fl[1], 64)
		return fmt.Sprintf("%.1f Cores", q/p)
	}
	return "None"
}
