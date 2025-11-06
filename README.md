# My GPU Exporter

A Prometheus exporter that exposes **per-process GPU energy consumption** for Kubernetes workloads.

## Key Features

- ✅ **ACTUAL Hardware-Measured Energy** - NOT estimates or approximations
- ✅ **Per-Process Attribution** - Accurate workload-level power consumption
- ✅ **Kubernetes Integration** - Automatic pod/namespace/container labeling
- ✅ **GPU Time-Slicing Support** - Each pod gets its own measured energy
- ✅ **Process Lifecycle Management** - Retains metrics after process exits
- ✅ **Prometheus Native** - Standard metrics and labels

## Critical: This Does NOT Estimate

**IMPORTANT:** This exporter uses DCGM's per-process energy API (`dcgm.GetProcessInfo().EnergyConsumed`) which provides **actual hardware-measured energy consumption**.

This is **NOT**:
- ❌ Estimated based on GPU utilization ratios
- ❌ Calculated by splitting GPU-level power
- ❌ Approximated using any formula
- ❌ Derived from proportional attribution

This **IS**:
- ✅ Hardware telemetry from the GPU
- ✅ Actual energy consumed by each process
- ✅ Measured by DCGM's accounting system
- ✅ Accurate per-process attribution

## Requirements

### Hardware
- NVIDIA GPU with Volta architecture or newer
- GPU must support DCGM per-process energy tracking

### Software
- NVIDIA Driver
- DCGM library (Data Center GPU Manager)
- **GPU Accounting Mode enabled**: `nvidia-smi -am 1` (must run as root)
- Kubernetes 1.20+ (for pod-resources API)

### Privileges
- Root access OR
- GPU accounting mode pre-enabled on all nodes

## Installation

### Kubernetes (Recommended)

**Note:** For OpenShift deployment, see [OpenShift Deployment Guide](docs/openshift-deployment.md).

1. **Create namespace and deploy:**
```bash
kubectl apply -f kubernetes/daemonset.yaml
```

The DaemonSet includes an init container that automatically enables GPU accounting mode.

2. **Verify deployment:**
```bash
kubectl -n gpu-monitoring get pods
kubectl -n gpu-monitoring logs -l app=my-gpu-exporter
```

3. **Check metrics:**
```bash
kubectl -n gpu-monitoring port-forward svc/my-gpu-exporter 9400:9400
curl http://localhost:9400/metrics
```

### Docker

```bash
# Build image
docker build -t my-gpu-exporter:latest .

# Run (requires GPU access and privileged mode)
docker run -d \
  --name my-gpu-exporter \
  --gpus all \
  --privileged \
  --pid=host \
  --network=host \
  -v /var/lib/kubelet/pod-resources:/var/lib/kubelet/pod-resources:ro \
  -v /proc:/proc:ro \
  my-gpu-exporter:latest
```

### Binary

```bash
# Build
make build

# Run (requires root for GPU access)
sudo ./my-gpu-exporter --log-level=info
```

## Configuration

### Command-line Flags

```bash
--dcgm-update-frequency=1s          # DCGM sampling frequency
--process-scan-interval=10s         # How often to scan for GPU processes
--kubernetes-enabled=true           # Enable Kubernetes pod mapping
--pod-resources-socket=/var/lib/kubelet/pod-resources/kubelet.sock
--metric-retention=5m               # Retain exited process metrics
--metric-prefix=my_gpu_process      # Prometheus metric name prefix
--listen-address=:9400              # HTTP server address
--metrics-path=/metrics             # Metrics endpoint path
--log-level=info                    # Log level (debug, info, warn, error)
```

## Metrics

All metrics include these labels:
- `pid` - Process ID
- `gpu` - GPU index
- `process_name` - Process executable name
- `pod` - Kubernetes pod name
- `namespace` - Kubernetes namespace
- `container` - Container name
- `pod_uid` - Pod UID
- `container_id` - Container ID

### Core Metrics

#### Energy (Counter)

```prometheus
my_gpu_process_energy_joules{pid="12345",gpu="0",pod="training-job",...} 15234.5
```

**IMPORTANT:** This is **ACTUAL measured energy** from GPU hardware, NOT estimated!

**Usage:**
```promql
# Average power in Watts
rate(my_gpu_process_energy_joules[1m])

# Total energy consumed in last hour (Joules)
increase(my_gpu_process_energy_joules{pod="training-job"}[1h])
```

#### Utilization (Gauges)

```prometheus
my_gpu_process_sm_utilization_ratio{...} 0.85
my_gpu_process_memory_utilization_ratio{...} 0.72
```

Values are 0.0-1.0 (0%-100%).

#### Memory (Gauge)

```prometheus
my_gpu_process_memory_used_bytes{...} 8589934592
```

GPU memory used by process in bytes.

#### Lifecycle (Gauges)

```prometheus
my_gpu_process_start_time_seconds{...} 1699564800
my_gpu_process_active{...} 1  # 1=running, 0=exited
```

## Example Queries

### Power Consumption

```promql
# Current power per pod (Watts)
rate(my_gpu_process_energy_joules{namespace="ml"}[1m])

# Total power across all pods
sum(rate(my_gpu_process_energy_joules[1m]))

# Power by namespace
sum by (namespace) (rate(my_gpu_process_energy_joules[5m]))
```

### Energy Accounting

```promql
# Energy consumed by pod in last hour (Joules)
increase(my_gpu_process_energy_joules{pod="training-job-123"}[1h])

# Convert to kWh
increase(my_gpu_process_energy_joules{pod="training-job-123"}[1h]) / 3600000

# Total energy cost (assuming $0.10/kWh)
(increase(my_gpu_process_energy_joules[1h]) / 3600000) * 0.10
```

### Efficiency

```promql
# Power efficiency (compute utilization per Watt)
my_gpu_process_sm_utilization_ratio / rate(my_gpu_process_energy_joules[1m])

# Most power-hungry pods
topk(10, rate(my_gpu_process_energy_joules[5m]))
```

### Active Processes

```promql
# Count of active GPU processes
sum(my_gpu_process_active)

# Active processes per namespace
sum by (namespace) (my_gpu_process_active)
```

## Comparison with dcgm-exporter

| Feature | dcgm-exporter | my-gpu-exporter |
|---------|---------------|-----------------|
| **Scope** | GPU-level | Process-level |
| **Power metric** | `DCGM_FI_DEV_POWER_USAGE` | `my_gpu_process_energy_joules` |
| **Time-slicing** | Duplicates same value | Separate measured values |
| **Estimation** | N/A (whole GPU) | **NO - uses actual measurements** |
| **Use case** | GPU monitoring | Workload cost attribution |

### Example with Time-Slicing

**dcgm-exporter** (both show 200W):
```prometheus
DCGM_FI_DEV_POWER_USAGE{gpu="0",exported_pod="pod-a"} 200
DCGM_FI_DEV_POWER_USAGE{gpu="0",exported_pod="pod-b"} 200
Sum = 400W (wrong - GPU only uses 200W!)
```

**my-gpu-exporter** (actual measured values):
```prometheus
my_gpu_process_energy_joules{gpu="0",pod="pod-a"} 120  # Actual: pod-a used 120J
my_gpu_process_energy_joules{gpu="0",pod="pod-b"} 80   # Actual: pod-b used 80J
Sum = 200J (correct!)
```

## Troubleshooting

### No metrics appearing

1. **Check GPU accounting mode:**
```bash
nvidia-smi -q | grep "Accounting Mode"
# Should show: Enabled
```

If disabled:
```bash
sudo nvidia-smi -am 1
```

2. **Check DCGM is working:**
```bash
dcgmi discovery -l
```

3. **Check exporter logs:**
```bash
kubectl -n gpu-monitoring logs -l app=my-gpu-exporter
```

### Energy values are zero

- GPU accounting mode must be enabled **before** processes start
- Restart GPU workloads after enabling accounting mode
- Wait 3+ seconds after process starts for DCGM to collect data

### "Failed to get container ID"

- Exporter needs access to `/proc/<pid>/cgroup`
- Ensure `hostPID: true` in DaemonSet
- Ensure `/proc` volume is mounted

### "Failed to get pod info"

- Check kubelet pod-resources socket exists:
```bash
ls -la /var/lib/kubelet/pod-resources/kubelet.sock
```

- Ensure socket is mounted in container
- Check Kubernetes version (requires 1.20+)

### Processes not showing up

- Exporter **only tracks Kubernetes pods**, not system processes
- Verify process is running in a container:
```bash
cat /proc/<pid>/cgroup
```

## Architecture

```
┌─────────────────────────────────────┐
│       my-gpu-exporter               │
├─────────────────────────────────────┤
│                                     │
│  ┌──────────────────────────────┐  │
│  │   NVML Process Discovery     │  │
│  │   (GetComputeRunningProcs)   │  │
│  └──────────────────────────────┘  │
│              │                      │
│              ▼                      │
│  ┌──────────────────────────────┐  │
│  │   DCGM Process Metrics       │  │
│  │   GetProcessInfo()           │  │
│  │   → EnergyConsumed (ACTUAL)  │  │
│  └──────────────────────────────┘  │
│              │                      │
│              ▼                      │
│  ┌──────────────────────────────┐  │
│  │   Kubernetes Pod Mapper      │  │
│  │   /proc/PID/cgroup           │  │
│  │   + Pod Resources API        │  │
│  └──────────────────────────────┘  │
│              │                      │
│              ▼                      │
│  ┌──────────────────────────────┐  │
│  │   Prometheus Exporter        │  │
│  │   /metrics endpoint          │  │
│  └──────────────────────────────┘  │
└─────────────────────────────────────┘
```

## Contributing

Contributions welcome! Please ensure:
- Code follows Go best practices
- Tests pass
- Documentation is updated
- No introduction of estimation or approximation (use actual measurements only)

## License

[Add your license here]

## Acknowledgments

- NVIDIA DCGM team for per-process energy API
- Prometheus community
- Kubernetes sig-node for pod-resources API
