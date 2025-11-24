# Time-Slicing Testing Guide

## Prerequisites

1. **GPU Time-Slicing Configuration**: Your cluster must have GPU time-slicing enabled
2. **GPU Accounting Mode**: Must be enabled (`nvidia-smi -am 1` on each node)
3. **my-gpu-exporter**: Deployed and running on GPU nodes

## Verify Time-Slicing Configuration

Check if time-slicing is enabled on your cluster:

```bash
# Check GPU Operator configuration (if using GPU Operator)
kubectl get clusterpolicy -o yaml | grep -A 10 "devicePlugin"

# Check node GPU capacity (should show replicas > physical GPUs)
kubectl get node <gpu-node> -o json | jq '.status.allocatable."nvidia.com/gpu"'

# Verify time-slicing ConfigMap
kubectl get cm -n gpu-operator time-slicing-config-all -o yaml
```

Expected time-slicing configuration:
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: time-slicing-config-all
  namespace: gpu-operator
data:
  any: |-
    version: v1
    sharing:
      timeSlicing:
        replicas: 4  # Number of slices per GPU
```

## Deploy Test Workload

Deploy 3 pods that will share GPU(s) via time-slicing:

```bash
# Deploy test workload
kubectl apply -f timeslicing-test.yaml

# Wait for pods to be running
kubectl wait --for=condition=Ready pod -l app=timeslice-test --timeout=60s

# Verify all 3 pods are running
kubectl get pods -l app=timeslice-test
```

Expected output:
```
NAME                              READY   STATUS    RESTARTS   AGE
timeslice-test-5b8c7d9f6b-abc12   1/1     Running   0          30s
timeslice-test-5b8c7d9f6b-def34   1/1     Running   0          30s
timeslice-test-5b8c7d9f6b-ghi56   1/1     Running   0          30s
```

## Verify GPU Allocation

Check if pods are sharing the same GPU:

```bash
# Get pod GPU assignments
for pod in $(kubectl get pods -l app=timeslice-test -o name); do
  echo "=== $pod ==="
  kubectl exec $pod -- nvidia-smi -L
  kubectl exec $pod -- nvidia-smi --query-compute-apps=pid,process_name,used_memory --format=csv
done
```

All pods should show the same GPU ID (e.g., all on GPU 0).

## Monitor Energy Metrics

### 1. Access Prometheus Metrics

```bash
# Port-forward to my-gpu-exporter
kubectl -n gpu-monitoring port-forward svc/my-gpu-exporter 9400:9400

# In another terminal, fetch metrics
curl -s http://localhost:9400/metrics | grep timeslice-test
```

### 2. Analyze Per-Process Energy

Look for metrics like:
```prometheus
my_gpu_process_energy_joules_total{pid="12345",gpu="0",pod="timeslice-test-5b8c7d9f6b-abc12",...} 1234.5
my_gpu_process_energy_joules_total{pid="12346",gpu="0",pod="timeslice-test-5b8c7d9f6b-def34",...} 567.8
my_gpu_process_energy_joules_total{pid="12347",gpu="0",pod="timeslice-test-5b8c7d9f6b-ghi56",...} 890.1
```

### 3. Calculate Per-Pod Power (Watts)

```bash
# Using Prometheus query (if you have Prometheus deployed)
# Power in Watts = rate of energy counter
curl -G 'http://prometheus:9090/api/v1/query' \
  --data-urlencode 'query=rate(my_gpu_process_energy_joules_total{app="timeslice-test"}[1m])'
```

Or use this PromQL query in Prometheus UI:
```promql
# Power consumption per pod (Watts)
rate(my_gpu_process_energy_joules_total{app="timeslice-test"}[1m])

# Total GPU power from all time-sliced pods
sum(rate(my_gpu_process_energy_joules_total{app="timeslice-test"}[1m])) by (gpu)
```

## Expected Behavior

### ✅ Correct Time-Slicing Support

Each pod should show:
- **Different energy values** - Each process accumulates its own energy
- **Energy sum ≤ GPU total** - Sum of all processes should not exceed GPU's total energy
- **Varying power levels** - Pods may have different power consumption based on their time slice utilization

Example:
```
Pod 1: 1200 J → 20 W average
Pod 2: 800 J  → 13 W average
Pod 3: 1000 J → 16 W average
Total: 3000 J → 49 W average (should match GPU power draw)
```

### ❌ Incorrect Behavior (Bug)

If you see:
- **All pods show same energy** - Indicates per-process accounting is not working
- **Energy sum > GPU total** - Indicates energy is duplicated across processes
- **All zeros** - GPU accounting mode not enabled or DCGM not collecting data

## Troubleshooting

### All pods show same energy value

This indicates DCGM is not properly tracking per-process energy.

**Check GPU accounting mode:**
```bash
nvidia-smi -q | grep "Accounting Mode"
```

Should show: `Accounting Mode: Enabled`

If disabled:
```bash
sudo nvidia-smi -am 1
```

**Restart processes after enabling:**
```bash
# Accounting mode must be enabled BEFORE processes start
kubectl delete pod -l app=timeslice-test
# Pods will be recreated by Deployment
```

### Energy values are all zero

**Check DCGM is collecting data:**
```bash
# Check exporter logs
kubectl -n gpu-monitoring logs -l app=my-gpu-exporter --tail=50

# Should see:
# "Retrieved process metrics" with non-zero energy values
```

**Wait for initial data collection:**
- DCGM needs 3-5 seconds after process starts to collect first samples
- Wait at least 10 seconds after pods start before checking metrics

### Pods not sharing GPU (spread across multiple GPUs)

**Verify time-slicing replicas:**
```bash
kubectl get node <gpu-node> -o json | \
  jq '.status.allocatable."nvidia.com/gpu"'
```

Should show replicas (e.g., "4") not physical GPU count (e.g., "1").

**Force pod to specific node:**
Add to timeslicing-test.yaml:
```yaml
spec:
  template:
    spec:
      nodeSelector:
        kubernetes.io/hostname: <specific-gpu-node>
```

## Cleanup

```bash
# Delete test workload
kubectl delete -f timeslicing-test.yaml

# Verify cleanup
kubectl get pods -l app=timeslice-test
```

## Next Steps

If time-slicing works correctly:
1. ✅ Document validated configuration
2. ✅ Update README with time-slicing prerequisites
3. ✅ Add time-slicing validation to exporter startup

If issues found:
1. ❌ Investigate DCGM per-process energy tracking with time-slicing
2. ❌ Check if additional DCGM configuration needed
3. ❌ Verify GPU driver support for per-process accounting with time-slicing
