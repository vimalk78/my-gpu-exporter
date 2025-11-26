# My GPU Exporter

A Prometheus exporter that exposes **per-process GPU energy consumption** for Kubernetes workloads.

## Key Features

- ✅ **Hardware-Measured Energy** - Direct from GPU hardware (when available)
- ✅ **SM-Based Estimation** - Automatic fallback when time-slicing detected
- ✅ **Per-Process Attribution** - Accurate workload-level power consumption
- ✅ **Kubernetes Integration** - Automatic pod/namespace/container labeling
- ✅ **GPU Time-Slicing Support** - Each pod gets its own measured/estimated energy
- ✅ **Process Lifecycle Management** - Retains metrics after process exits
- ✅ **Prometheus Native** - Standard metrics and labels

## Energy Measurement: Hardware vs Estimation

This exporter prioritizes **hardware-measured energy** from DCGM but automatically falls back to **SM-based estimation** when needed.

### Hardware-Measured Energy (Default)

**When single process per GPU:**
- ✅ Uses DCGM's per-process energy API (`dcgm.GetProcessInfo().EnergyConsumed`)
- ✅ Hardware telemetry from the GPU
- ✅ Actual energy consumed by each process
- ✅ Most accurate attribution
- ✅ Label: `energy_estimated="false"`

### SM-Based Estimation (Fallback)

**When time-slicing detected with DCGM bug (identical energy values):**
- ✅ Automatically detects the issue
- ✅ Estimates energy using SM utilization ratios
- ✅ Formula: `process_energy = gpu_power × (process_sm_util / total_sm_util)`
- ✅ Logs estimation mode for transparency
- ✅ Label: `energy_estimated="true"`
- ℹ️ Enable/disable with `--enable-energy-estimation` (enabled by default)

**Why estimation is needed:** DCGM has a bug where all time-sliced processes report identical energy values (the GPU total). See [DCGM Time-Slicing Energy Bug](docs/DCGM-TIME-SLICING-ENERGY-BUG.md) for details.

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
--enable-energy-estimation=true     # Enable SM-based estimation for time-slicing
--listen-address=:9400              # HTTP server address
--metrics-path=/metrics             # Metrics endpoint path
--log-level=info                    # Log level (debug, info, warn, error)
```

## Metrics

### Per-Process Metrics

All per-process metrics include these labels:
- `pid` - Process ID
- `gpu` - GPU index
- `process_name` - Process executable name
- `pod` - Kubernetes pod name
- `namespace` - Kubernetes namespace
- `container` - Container name
- `container_id` - Container ID

#### Energy (Counter)

```prometheus
# Hardware-measured (single process)
my_gpu_process_energy_joules_total{...,energy_estimated="false"} 15234.5

# SM-based estimation (time-slicing with DCGM bug)
my_gpu_process_energy_joules_total{...,energy_estimated="true"} 8421.2
```

**Energy Source:**
- `energy_estimated="false"` - Hardware-measured from GPU (most accurate)
- `energy_estimated="true"` - SM-based estimation (automatic fallback for time-slicing)

**Query to filter by energy source:**
```promql
# Only hardware-measured energy
my_gpu_process_energy_joules_total{energy_estimated="false"}

# Only estimated energy
my_gpu_process_energy_joules_total{energy_estimated="true"}
```

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

### GPU-Level Aggregation Metrics

These metrics aggregate per-process data at the GPU level, useful for validating time-slicing:

#### Total GPU Energy (Counter)

```prometheus
my_gpu_process_gpu_energy_joules_total{gpu="0"} 45234.5
```

Sum of energy consumed by all processes on this GPU. With time-slicing, this represents the total GPU energy distributed across multiple processes.

#### GPU Process Count (Gauge)

```prometheus
my_gpu_process_gpu_process_count{gpu="0"} 3
```

Number of active processes on this GPU. When `> 1`, indicates time-slicing is active.

**Usage:**
```promql
# Detect time-slicing (GPUs with multiple processes)
my_gpu_process_gpu_process_count > 1

# Total power per GPU (Watts)
rate(my_gpu_process_gpu_energy_joules_total[1m])

# Verify: GPU total should equal sum of per-process
rate(my_gpu_process_gpu_energy_joules_total{gpu="0"}[1m])
==
sum(rate(my_gpu_process_energy_joules_total{gpu="0"}[1m]))
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

## Time-Slicing Support

my-gpu-exporter **fully supports GPU time-slicing** with automatic detection and intelligent energy attribution:

### Features

1. **Automatic Detection**: Detects when multiple processes share a GPU
2. **Smart Energy Attribution**:
   - **Hardware-measured** (preferred): Uses DCGM when values are differentiated
   - **SM-based estimation** (fallback): Automatically applied when DCGM reports identical values (bug)
3. **Transparent Labeling**: `energy_estimated` label indicates measurement method
4. **Validation**: Detects and logs DCGM time-slicing bug
5. **Aggregation Metrics**: GPU-level totals for validation

### Testing Time-Slicing

See [Time-Slicing Testing Guide](docs/TIMESLICING-TEST.md) for comprehensive testing instructions.

Quick validation:
```bash
# Deploy test workload (3 pods sharing GPU)
kubectl apply -f timeslicing-test.yaml

# Check metrics show different energy per process
curl http://exporter:9400/metrics | grep energy_joules_total

# Verify process count > 1 (indicates time-slicing)
curl http://exporter:9400/metrics | grep gpu_process_count
```

### Logs

**When time-slicing detected with proper DCGM values:**
```
INFO Time-slicing detected: multiple processes on same GPU gpu=0 process_count=3
DEBUG Time-slicing validation: energy values properly differentiated gpu=0 process_count=3
```

**When DCGM bug detected (identical values) and estimation is applied:**
```
INFO Time-slicing detected: multiple processes on same GPU gpu=0 process_count=3
INFO Applying SM-based energy estimation for time-sliced processes gpu=0 process_count=3
DEBUG Applied energy estimation pid=12345 pod=training-job sm_util=0.39 proportion=0.78 estimated_energy_J=245.3
```

**If estimation is disabled:**
```
WARN SUSPICIOUS: All time-sliced processes show identical energy values (estimation disabled) hint="Enable --enable-energy-estimation to use SM-based estimation"
```

## Comparison with dcgm-exporter

| Feature | dcgm-exporter | my-gpu-exporter |
|---------|---------------|-----------------|
| **Scope** | GPU-level | Process-level |
| **Power metric** | `DCGM_FI_DEV_POWER_USAGE` | `my_gpu_process_energy_joules` |
| **Time-slicing** | Duplicates same value | Smart attribution (measured or estimated) |
| **Time-slice detection** | No | Yes (automatic) |
| **DCGM bug detection** | No | Yes (with auto-fallback) |
| **Energy attribution** | N/A (whole GPU) | Hardware-measured (preferred), SM-estimated (fallback) |
| **Transparency** | N/A | `energy_estimated` label shows method |
| **Use case** | GPU monitoring | Workload cost attribution |

### Example with Time-Slicing

**dcgm-exporter** (both show 200W):
```prometheus
DCGM_FI_DEV_POWER_USAGE{gpu="0",exported_pod="pod-a"} 200
DCGM_FI_DEV_POWER_USAGE{gpu="0",exported_pod="pod-b"} 200
Sum = 400W (wrong - GPU only uses 200W!)
```

**my-gpu-exporter** (intelligent attribution):
```prometheus
# Scenario 1: DCGM provides correct per-process values (hardware-measured)
my_gpu_process_energy_joules_total{gpu="0",pod="pod-a",energy_estimated="false"} 120
my_gpu_process_energy_joules_total{gpu="0",pod="pod-b",energy_estimated="false"} 80

# Scenario 2: DCGM bug detected, SM-based estimation applied
# (pod-a has 60% SM util, pod-b has 40% SM util, GPU power is 200W)
my_gpu_process_energy_joules_total{gpu="0",pod="pod-a",energy_estimated="true"} 120
my_gpu_process_energy_joules_total{gpu="0",pod="pod-b",energy_estimated="true"} 80

# GPU-level aggregation (always correct)
my_gpu_process_gpu_energy_joules_total{gpu="0"} 200

# Process count (indicates time-slicing)
my_gpu_process_gpu_process_count{gpu="0"} 2
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
