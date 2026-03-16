# K8s Demo: Deployments & StatefulSets
**Resilience and Storage Patterns**

This demo walks through Kubernetes concepts including Deployments, StatefulSets, health probes, graceful shutdown, and persistent storage patterns.

---

## Prerequisites

### Terminal Setup
Open **TWO terminals** side-by-side:
- **Terminal 1 (LEFT)**: Watch resources continuously
- **Terminal 2 (RIGHT)**: Run commands

**Terminal 1:**
```bash
watch -n 1 "oc get pod,replicaset,deployment,statefulset,service,pvc,route -l 'app in (deployment-web,statefulset-web)'"
```

### RBAC Permissions
**Terminal 2:**

Required for pods to introspect Kubernetes API:
```bash
oc adm policy add-role-to-user view -z default
```

---

## Part 1: Deployment Basics

### 1.1 Create Deployment

```bash
oc apply -f deployment.yaml
```

**Observe (Terminal 1):**
- Deployment created
- ReplicaSet created automatically (random suffix)
- 3 Pods created **SIMULTANEOUSLY** with random names
  - Example: `deployment-web-7d4f8b9c-abc12`, `-def34`, `-ghi56`
- Service and Route created

Wait for pods to be ready (1/1)

### 1.2 Access the Dashboard

```bash
ROUTE=$(oc get route route-deployment -o jsonpath='{.spec.host}')
echo "📊 Open Dashboard: http://$ROUTE"
```

**Explore the UI:**
- See the deployment hierarchy (Deployment → ReplicaSet → Pods)
- Current pod is **HIGHLIGHTED**
- CPU, Memory, Throttling metrics
- Live auto-refresh every 1 second

### 1.3 Demonstrate Self-Healing

Delete one pod:
```bash
POD_TO_DELETE=$(oc get pod -l app=deployment-web -o jsonpath='{.items[0].metadata.name}')
echo "Deleting: $POD_TO_DELETE"
oc delete pod $POD_TO_DELETE
```

**Observe:**
- Pod immediately replaced
- **NEW** pod has different random name
- Total count stays at 3/3
- In dashboard: old pod shows "Terminating", new pod appears

---

## Part 2: Production Best Practices

### 2.1 Add Resource Requests and Limits

```bash
oc set resources deployment/deployment-web \
  --limits=memory=512Mi,cpu=500m \
  --requests=cpu=25m,memory=256Mi
```

**Observe:**
- **NEW** ReplicaSet created (different pod template hash)
- Gradual rollout: 1 new pod created, 1 old terminated
- Rolling update preserves 3 total pods during transition
- In dashboard: CPU limit now shows "0.5 Cores"

### 2.2 Add Health Probes

**BEST PRACTICE: Pause rollouts when making multiple changes**

Each `oc set` command that modifies the pod template triggers a new rollout, which restarts all pods. Without pausing, running multiple commands would cause:
- Multiple rollouts (pods restart multiple times)
- Increased disruption to running services
- Longer total time to apply all changes

By pausing the deployment, making all changes, then resuming, you get:
- **ONE** rollout with all changes batched together
- Pods restart only once
- More efficient and less disruptive to production workloads

Pause the rollout, add both probes, then resume:
```bash
# Pause the rollout to batch multiple configuration changes together
oc rollout pause deployment/deployment-web

# Add readiness probe
oc set probe deployment/deployment-web \
  --readiness --failure-threshold=2 --initial-delay-seconds=10 --period-seconds=20 \
  --get-url=http://:8080/readyz

# Add liveness probe
oc set probe deployment/deployment-web \
  --liveness --failure-threshold=2 --initial-delay-seconds=10 --period-seconds=20 \
  --get-url=http://:8080/healthz

# Resume the rollout - all changes above will be applied in a single rollout
oc rollout resume deployment/deployment-web
```

**Observe:**
- Rollout was paused - no immediate pod changes when probes were added
- After resume, **ONE** new ReplicaSet created with both probes configured
- Single rolling update applies all changes at once
- More efficient than two separate rollouts

### 2.3 Verify Probes Work

Check probe configuration:
```bash
oc describe deployment deployment-web | grep -A 5 "Liveness\|Readiness"
```

Test endpoints manually:
```bash
POD=$(oc get pod -l app=deployment-web -o jsonpath='{.items[0].metadata.name}')
oc exec $POD -- curl -s http://localhost:8080/healthz && echo "✓ Liveness OK"
oc exec $POD -- curl -s http://localhost:8080/readyz && echo "✓ Readiness OK"
```

### 2.4 INTERACTIVE: Test Resilience Features

**In the dashboard (browser):**

#### EXPERIMENT 1: CPU Stress
- Click **"Stress CPU"** button
- Watch CPU percentage spike to ~100%
- Observe throttling counter increase
- Pod stays healthy

#### EXPERIMENT 2: Readiness Failure
- Click **"Simulate Load"** button
- Pod becomes "Not Ready" (0/1)
- Pod removed from service but **NOT** restarted
- After 60s, automatically recovers (1/1)

#### EXPERIMENT 3: Liveness Failure
- Click **"Break Health"** button
- Wait ~20-30 seconds
- Pod **RESTARTS** (container killed and recreated)
- Restart counter increments
- Pod name **STAYS THE SAME** (container restart, not pod replacement)

### 2.5 Demonstrate Graceful Shutdown (SIGTERM)

The application handles SIGTERM gracefully with a 15-second shutdown window. To see this in action, you need to delete the pod you're currently viewing.

**STEPS:**
1. In the browser dashboard, look at the pod table
2. The row with the **ORANGE/HIGHLIGHTED** background is YOUR current pod
3. Note the pod name (e.g., `deployment-web-abc123-xyz45`)
4. Run the command below, replacing `<pod-name>` with your highlighted pod

The sleep gives you 3 seconds to switch back to the browser to watch:

```bash
# Replace <pod-name> with the highlighted pod from the dashboard
sleep 3 && oc delete pod <pod-name>
```

**Observe in Browser:**
- Immediately see "🛑 **SIGTERM RECEIVED**" overlay appear
- Message shows: "Pod is Terminating. Removing from LoadBalancer. Finishing work (15s)..."
- This demonstrates graceful shutdown - the app:
  1. Receives SIGTERM signal from Kubernetes
  2. Stops accepting new requests (readiness probe fails)
  3. Continues processing existing requests for 15 seconds
  4. Cleanly exits

**KEY POINT:** Graceful shutdown prevents dropped requests during pod termination. In production, this would be coordinated with:
- preStop hooks
- terminationGracePeriodSeconds (default 30s)
- Load balancer connection draining

---

## Part 3: Persistent Storage (Deployment)

### 3.1 Create Shared PVC (ReadWriteMany)

```bash
oc apply -f pvc-web-deployment.yaml
```

Wait for PVC to bind:
```bash
oc get pvc pvc-web-deployment
```

**Observe:**
- PVC uses "ReadWriteMany" access mode
- Backed by EFS storage class (shared file storage)
- Can be mounted by **MULTIPLE** pods simultaneously

### 3.2 Attach PVC to Deployment

```bash
oc set volume deployment/deployment-web \
  --add \
  --name=volume-web \
  -t pvc \
  --claim-name=pvc-web-deployment \
  --mount-path=/mnt
```

**Observe:**
- Yet another rollout (volume added to pod template)

### 3.3 Demonstrate Shared Storage

Write data from first pod:
```bash
POD1=$(oc get pod -l app=deployment-web -o jsonpath='{.items[0].metadata.name}')
echo "Writing from: $POD1"
oc exec $POD1 -- sh -c 'echo "Hello from $HOSTNAME at $(date)" > /mnt/shared.txt'
```

Read from second pod (different pod, **SAME** volume):
```bash
POD2=$(oc get pod -l app=deployment-web -o jsonpath='{.items[1].metadata.name}')
echo "Reading from: $POD2"
oc exec $POD2 -- cat /mnt/shared.txt
```

**KEY POINT:** Multiple pods share the same file!

Delete the pod that wrote the file:
```bash
oc delete pod $POD1
```

Wait for replacement, then read again (data persists):
```bash
sleep 10
POD_NEW=$(oc get pod -l app=deployment-web -o jsonpath='{.items[0].metadata.name}')
echo "Reading from new pod: $POD_NEW"
oc exec $POD_NEW -- cat /mnt/shared.txt
```

**Observe:** Data survives pod deletion (persistent storage)

---

## Part 4: StatefulSet Comparison

### 4.1 Clean Up Deployment Resources

```bash
oc delete -f deployment.yaml
oc delete pvc pvc-web-deployment
```

**Observe:** All deployment pods terminate simultaneously

### 4.2 Create StatefulSet

```bash
oc apply -f statefulset.yaml
```

**Observe carefully (this is different!):**
- Pods created **SEQUENTIALLY**: 0, then 1, then 2
- Each pod waits for previous to be Ready before starting
- Pod names are **ORDERED** and **STABLE**:
  - `statefulset-web-0`
  - `statefulset-web-1`
  - `statefulset-web-2`
- Each pod gets its **OWN** PVC:
  - `statefulset-web-statefulset-web-0`
  - `statefulset-web-statefulset-web-1`
  - `statefulset-web-statefulset-web-2`
- PVCs use "ReadWriteOnce" (single pod access)

### 4.3 Access StatefulSet Dashboard

```bash
ROUTE_SS=$(oc get route route-statefulset -o jsonpath='{.spec.host}')
echo "📊 StatefulSet Dashboard: http://$ROUTE_SS"
```

**Notice in UI:**
- Shows "StatefulSet" instead of "Deployment"
- No ReplicaSet layer (StatefulSets manage pods directly)
- Pod names are predictable

### 4.4 Add Probes to StatefulSet

```bash
oc set probe statefulset/statefulset-web \
  --liveness --initial-delay-seconds=5 --period-seconds=20 \
  --get-url=http://:8080/healthz

oc set probe statefulset/statefulset-web \
  --readiness --initial-delay-seconds=5 --period-seconds=20 \
  --get-url=http://:8080/readyz
```

**Observe:**
- Pods updated in **REVERSE** order: 2 → 1 → 0
- Each waits for previous to be Ready
- Ordered, controlled rollout

### 4.5 Demonstrate Stable Identity

Delete the middle pod:
```bash
oc delete pod statefulset-web-1
```

**Observe:**
- Replacement pod gets **EXACT SAME NAME**: `statefulset-web-1`
- Reconnects to **SAME** PVC: `statefulset-web-statefulset-web-1`
- Stable network identity preserved

Verify PVC attachment:
```bash
oc get pod statefulset-web-1 -o jsonpath='{.spec.volumes[0].persistentVolumeClaim.claimName}'
# Should show: statefulset-web-statefulset-web-1
```

### 4.6 Demonstrate Individual Storage

Write different data to each pod's volume:
```bash
oc exec statefulset-web-0 -- sh -c 'echo "I am pod-0" > /opt/app-root/src/identity.txt'
oc exec statefulset-web-1 -- sh -c 'echo "I am pod-1" > /opt/app-root/src/identity.txt'
oc exec statefulset-web-2 -- sh -c 'echo "I am pod-2" > /opt/app-root/src/identity.txt'
```

Delete all pods:
```bash
oc delete pod -l app=statefulset-web
```

Wait for recreation, then verify each pod kept its own data:
```bash
sleep 20
oc exec statefulset-web-0 -- cat /opt/app-root/src/identity.txt  # "I am pod-0"
oc exec statefulset-web-1 -- cat /opt/app-root/src/identity.txt  # "I am pod-1"
oc exec statefulset-web-2 -- cat /opt/app-root/src/identity.txt  # "I am pod-2"
```

**KEY POINT:** Each pod maintains its own persistent state

---

## Summary: Deployment vs StatefulSet

### Deployment (Stateless Applications)
✓ Random pod names
✓ Parallel creation/deletion
✓ No guaranteed order
✓ Shared storage (RWX) or no storage
✓ Pods are interchangeable

**Use for:** Web servers, API services, workers

### StatefulSet (Stateful Applications)
✓ Ordered, stable pod names (0, 1, 2...)
✓ Sequential creation, reverse deletion
✓ Each pod gets unique persistent volume
✓ Stable network identity
✓ Pods are NOT interchangeable

**Use for:** Databases, message queues, ZooKeeper, Kafka

---

## Cleanup

Remove StatefulSet:
```bash
oc delete -f statefulset.yaml
```

StatefulSet PVCs are **NOT** auto-deleted (data safety). Must delete manually:
```bash
oc delete pvc -l app=statefulset-web
```

Verify everything is gone:
```bash
oc get all,pvc -l 'app in (deployment-web,statefulset-web)'
# Should show: No resources found
```

---

## Troubleshooting

### Pods not starting?
```bash
oc describe pod <pod-name>
oc get events --sort-by='.lastTimestamp'
```

### Dashboard not accessible?
```bash
oc get route
# Check route host is correct
```

### RBAC errors in pod logs?
```bash
oc adm policy add-role-to-user view -z default
```

### Image pull errors?
```bash
oc describe pod <pod-name> | grep -A 5 Events
```

### Probe failures?
```bash
POD=<pod-name>
oc exec $POD -- curl -v http://localhost:8080/healthz
oc exec $POD -- curl -v http://localhost:8080/readyz
oc logs $POD
```
