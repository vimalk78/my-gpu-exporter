# Deployment Guide

## Prerequisites

Before deploying my-gpu-exporter, ensure:

1. **NVIDIA GPUs** with Volta architecture or newer
2. **NVIDIA Driver** installed on all GPU nodes
3. **DCGM libraries** installed (`datacenter-gpu-manager` package)
4. **Kubernetes 1.20+** (for pod-resources API)
5. **GPU nodes labeled**: `nvidia.com/gpu=true`

## Step 1: Enable GPU Accounting Mode

GPU accounting mode **must** be enabled for per-process energy tracking to work.

### Option A: Via Init Container (Recommended)

The provided DaemonSet includes an init container that automatically enables accounting mode. No manual action needed.

### Option B: Manual Enable (One-time setup)

Run on each GPU node:
```bash
sudo nvidia-smi -am 1
```

Verify:
```bash
nvidia-smi -q | grep "Accounting Mode"
# Should show: Enabled
```

**Note:** Accounting mode persists across reboots on most systems.

## Step 2: Build Docker Image

### Local Build

```bash
cd /home/vimalkum/src/powermon/nvidia/my-gpu-exporter
make docker-build
```

### Push to Registry

```bash
export DOCKER_IMAGE=your-registry/my-gpu-exporter
export DOCKER_TAG=v1.0.0

docker build -t $DOCKER_IMAGE:$DOCKER_TAG .
docker push $DOCKER_IMAGE:$DOCKER_TAG
```

Update `kubernetes/daemonset.yaml` with your image:
```yaml
image: your-registry/my-gpu-exporter:v1.0.0
```

## Step 3: Deploy to Kubernetes

### Create Namespace and Deploy

```bash
kubectl apply -f kubernetes/daemonset.yaml
```

This creates:
- `Namespace`: `gpu-monitoring`
- `ServiceAccount`: `my-gpu-exporter`
- `DaemonSet`: `my-gpu-exporter` (runs on GPU nodes)
- `Service`: `my-gpu-exporter` (ClusterIP on port 9400)

### Verify Deployment

```bash
# Check pods are running
kubectl -n gpu-monitoring get pods -l app=my-gpu-exporter

# Check logs
kubectl -n gpu-monitoring logs -l app=my-gpu-exporter --tail=50

# Check metrics endpoint
kubectl -n gpu-monitoring port-forward svc/my-gpu-exporter 9400:9400
curl http://localhost:9400/metrics | grep my_gpu_process
```

## Step 4: Configure Prometheus

Add the following to your Prometheus configuration:

```yaml
scrape_configs:
  - job_name: 'my-gpu-exporter'
    kubernetes_sd_configs:
      - role: endpoints
        namespaces:
          names:
            - gpu-monitoring
    relabel_configs:
      - source_labels: [__meta_kubernetes_service_name]
        action: keep
        regex: my-gpu-exporter
      - source_labels: [__meta_kubernetes_endpoint_port_name]
        action: keep
        regex: metrics
```

Or use a ServiceMonitor (Prometheus Operator):

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: my-gpu-exporter
  namespace: gpu-monitoring
spec:
  selector:
    matchLabels:
      app: my-gpu-exporter
  endpoints:
  - port: metrics
    interval: 30s
```

## Step 5: Verify Metrics in Prometheus

Query Prometheus to verify metrics are being scraped:

```promql
# Check if metrics exist
up{job="my-gpu-exporter"}

# Check per-process energy
my_gpu_process_energy_joules

# Check active processes
sum(my_gpu_process_active)
```

## Configuration Options

### DaemonSet Environment Variables

Edit `kubernetes/daemonset.yaml`:

```yaml
env:
  # Enable debug logging
  - name: LOG_LEVEL
    value: "debug"
```

### Command-line Arguments

```yaml
args:
  - --log-level=info
  - --kubernetes-enabled=true
  - --pod-resources-socket=/var/lib/kubelet/pod-resources/kubelet.sock
  - --metric-retention=5m
  - --process-scan-interval=10s
  - --listen-address=:9400
```

### Resource Limits

Adjust based on your cluster size:

```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

## Security Considerations

### Privileged Container

The exporter requires privileged mode to:
- Access GPU devices
- Read `/proc` for cgroup information
- Query GPU metrics via DCGM

**Important:** This grants significant permissions. Consider:
1. Running only on trusted nodes
2. Using Pod Security Policies/Standards
3. Network policies to restrict access

### Alternative: Pre-enable Accounting

If you don't want the init container running `nvidia-smi` as privileged:

1. Manually enable accounting on all nodes (one-time):
```bash
ansible all -i gpu-nodes -b -m shell -a "nvidia-smi -am 1"
```

2. Remove init container from DaemonSet

3. Main container still needs privileged mode for GPU/proc access

## Monitoring the Exporter

### Health Checks

```bash
# Liveness
curl http://<pod-ip>:9400/health

# Metrics
curl http://<pod-ip>:9400/metrics
```

### Logs

```bash
# Follow logs
kubectl -n gpu-monitoring logs -f -l app=my-gpu-exporter

# Check for errors
kubectl -n gpu-monitoring logs -l app=my-gpu-exporter | grep -i error
```

### Common Log Messages

**Success:**
```
INFO Initializing collector
INFO Registered Prometheus exporter
INFO Exporter started successfully metrics_url=http://:9400/metrics
```

**Errors:**
```
ERROR Failed to create DCGM client
→ Check DCGM libraries are installed

ERROR Failed to get container ID
→ Check hostPID=true and /proc is mounted

ERROR Failed to get pod info
→ Check pod-resources socket path
```

## Testing

### Test with Sample Workload

Deploy a GPU workload:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test
  namespace: default
spec:
  containers:
  - name: cuda-test
    image: nvidia/cuda:12.3.1-base-ubuntu22.04
    command: ["bash", "-c"]
    args:
      - |
        # Simple GPU workload
        nvidia-smi
        while true; do
          nvidia-smi dmon -s u -c 1
          sleep 5
        done
    resources:
      limits:
        nvidia.com/gpu: 1
```

Query metrics:

```promql
# Check pod appears
my_gpu_process_active{pod="gpu-test"}

# Check energy consumption
rate(my_gpu_process_energy_joules{pod="gpu-test"}[1m])
```

## Troubleshooting

### No Metrics for Processes

1. **Check accounting mode:**
```bash
nvidia-smi -q | grep "Accounting Mode"
```

2. **Check process is containerized:**
```bash
# Get PID from nvidia-smi
nvidia-smi --query-compute-apps=pid --format=csv,noheader

# Check cgroup
cat /proc/<PID>/cgroup
```

3. **Check exporter can see process:**
```bash
kubectl -n gpu-monitoring logs -l app=my-gpu-exporter | grep "PID.*<pid>"
```

### Metrics Show Zero Energy

- Accounting mode must be enabled **before** process starts
- Restart GPU workloads after enabling accounting
- Wait 3+ seconds after process starts for DCGM data collection

### Permission Denied Errors

Check:
1. `securityContext.privileged: true` in DaemonSet
2. `hostPID: true` in DaemonSet spec
3. `/proc` volume mounted correctly

### High Memory Usage

If exporter uses too much memory:
1. Increase `--process-scan-interval` (default: 10s)
2. Reduce `--metric-retention` (default: 5m)
3. Filter specific namespaces in collector code

## Upgrading

### Rolling Update

```bash
# Update image in DaemonSet
kubectl -n gpu-monitoring set image daemonset/my-gpu-exporter \
  exporter=your-registry/my-gpu-exporter:v1.1.0

# Watch rollout
kubectl -n gpu-monitoring rollout status daemonset/my-gpu-exporter
```

### Configuration Changes

```bash
# Edit DaemonSet
kubectl -n gpu-monitoring edit daemonset my-gpu-exporter

# Restart pods
kubectl -n gpu-monitoring rollout restart daemonset/my-gpu-exporter
```

## Uninstalling

```bash
kubectl delete -f kubernetes/daemonset.yaml
```

Or:

```bash
kubectl delete namespace gpu-monitoring
```

## Production Checklist

Before deploying to production:

- [ ] GPU accounting mode enabled on all nodes
- [ ] Docker image pushed to registry
- [ ] Resource limits configured appropriately
- [ ] Prometheus scrape configured
- [ ] Alerts configured (high power, low utilization)
- [ ] Grafana dashboards created
- [ ] Security policies reviewed
- [ ] Tested with sample workloads
- [ ] Verified metrics accuracy
- [ ] Documented for your team

## Performance Impact

Expected resource usage:
- **CPU**: 50-200m (varies with process count)
- **Memory**: 100-300Mi (varies with process count)
- **Network**: Minimal (Prometheus scrapes only)
- **GPU overhead**: Negligible (DCGM is lightweight)

## Next Steps

1. **Create Grafana Dashboard** - Visualize per-pod power consumption
2. **Set up Alerts** - Notify on high power or inefficiency
3. **Cost Attribution** - Use metrics for billing/showback
4. **Capacity Planning** - Analyze power trends over time
5. **Optimization** - Identify and tune power-hungry workloads
