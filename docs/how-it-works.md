# How my-gpu-exporter Works

## Per-Process GPU Energy Attribution

### Single Process per GPU
When only one process uses a GPU, DCGM provides **actual hardware-measured energy** via `dcgm.GetProcessInfo()`:

```go
info.ProcessUtilization.EnergyConsumed  // Joules, measured by GPU hardware
```

### Multiple Processes (Time-Slicing)
When multiple processes share a GPU via time-slicing, DCGM attributes all energy to one process (incorrect). We detect this and use **SM-based estimation**:

```
process_energy = GPU_power × interval × (process_sm_util / total_sm_util)
```

Example with 2 processes:
- GPU power: 70W
- Process A SM util: 80%
- Process B SM util: 20%
- Total SM util: 100%
- Interval: 10s

```
Process A: 70W × 10s × (0.8/1.0) = 560 J
Process B: 70W × 10s × (0.2/1.0) = 140 J
```

## Data Flow

```
┌─────────────────┐     ┌──────────────┐     ┌─────────────────┐
│  NVML/DCGM      │────▶│  Collector   │────▶│  Prometheus     │
│  - GPU power    │     │  - Detect    │     │  /metrics       │
│  - SM util      │     │    time-     │     │                 │
│  - Process PIDs │     │    slicing   │     │                 │
└─────────────────┘     │  - Estimate  │     └─────────────────┘
                        │    energy    │
┌─────────────────┐     │              │
│  /proc/<pid>/   │────▶│              │
│  cgroup         │     └──────────────┘
│  - Container ID │            │
│  - Pod UID      │            ▼
└─────────────────┘     ┌──────────────┐
                        │  K8s API     │
                        │  - Pod name  │
                        │  - Namespace │
                        │  - Container │
                        └──────────────┘
```

## Key Components

| Component | Source | Purpose |
|-----------|--------|---------|
| GPU Power | `DCGM_FI_DEV_POWER_USAGE` | Current power draw (Watts) |
| SM Utilization | `dcgm.GetProcessInfo().SmUtil` | Per-process compute usage |
| Container ID | `/proc/<pid>/cgroup` | Links process to container |
| Pod UID | `/proc/<pid>/cgroup` | Links container to K8s pod |
| Pod Metadata | K8s API `/api/v1/pods` | Pod name, namespace, container name |

## Exported Metrics

```prometheus
# Energy counter (Joules) - monotonically increasing
my_gpu_process_energy_joules_total{
  pod="gpu-heavy-xyz",
  namespace="default",
  container="pytorch",
  gpu="0",
  energy_estimated="true"  # "false" if single process (DCGM measured)
} 107973.09

# SM utilization (0.0-1.0)
my_gpu_process_sm_utilization_ratio{...} 0.8

# Memory usage (bytes)
my_gpu_process_memory_used_bytes{...} 4294967296
```

## Time-Slicing Detection

```go
// Count active processes per GPU
for _, pm := range processMetrics {
    if pm.IsRunning {
        gpuProcesses[pm.GPU] = append(gpuProcesses[pm.GPU], pm)
    }
}

// If multiple processes on same GPU -> time-slicing
if len(processes) > 1 {
    applyEnergyEstimation(gpuID, processes)
}
```
