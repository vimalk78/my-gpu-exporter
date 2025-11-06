# Metrics Reference

This document provides a complete reference for all metrics exported by my-gpu-exporter.

## Important: No Estimation

**All energy values are ACTUAL hardware-measured values from GPU telemetry, NOT estimated or calculated.**

## Common Labels

All metrics include these labels:

| Label | Description | Example |
|-------|-------------|---------|
| `pid` | Process ID | `12345` |
| `gpu` | GPU index (0-based) | `0` |
| `process_name` | Process executable name | `python` |
| `pod` | Kubernetes pod name | `training-job-abc123` |
| `namespace` | Kubernetes namespace | `ml-workloads` |
| `container` | Container name | `trainer` |
| `pod_uid` | Pod UID | `a1b2c3d4-...` |
| `container_id` | Container ID | `cri-containerd-...` |

## Metrics

### my_gpu_process_energy_joules

**Type:** Counter (cumulative)

**Description:** ACTUAL hardware-measured cumulative energy consumed by process in Joules.

**IMPORTANT:** This is **NOT** estimated. The value comes directly from `dcgm.GetProcessInfo().EnergyConsumed`, which reads GPU hardware counters.

**Unit:** Joules (J)

**Usage:**

```promql
# Convert to Watts (power)
rate(my_gpu_process_energy_joules[1m])

# Energy consumed in last hour
increase(my_gpu_process_energy_joules{pod="my-pod"}[1h])

# Convert to kWh
increase(my_gpu_process_energy_joules[1h]) / 3600000
```

**Example:**
```prometheus
my_gpu_process_energy_joules{pid="12345",gpu="0",process_name="python",pod="training-job",namespace="ml",container="trainer",pod_uid="...",container_id="..."} 15234.567
```

**Notes:**
- Counter starts at 0 when process starts
- Increases monotonically while process runs
- Use `rate()` to get power in Watts
- Use `increase()` to get energy over a time range

---

### my_gpu_process_sm_utilization_ratio

**Type:** Gauge

**Description:** SM (Streaming Multiprocessor) utilization ratio.

**Unit:** Ratio (0.0-1.0, representing 0%-100%)

**Usage:**

```promql
# Current SM utilization
my_gpu_process_sm_utilization_ratio{pod="my-pod"}

# Average utilization over 5 minutes
avg_over_time(my_gpu_process_sm_utilization_ratio{pod="my-pod"}[5m])

# Identify underutilized processes
my_gpu_process_sm_utilization_ratio < 0.2
```

**Example:**
```prometheus
my_gpu_process_sm_utilization_ratio{pid="12345",gpu="0",...} 0.85
```

**Notes:**
- 0.0 = 0% (idle)
- 1.0 = 100% (fully utilized)
- Represents compute core utilization
- High values indicate compute-bound workloads

---

### my_gpu_process_memory_utilization_ratio

**Type:** Gauge

**Description:** Memory bandwidth utilization ratio.

**Unit:** Ratio (0.0-1.0, representing 0%-100%)

**Usage:**

```promql
# Current memory utilization
my_gpu_process_memory_utilization_ratio{pod="my-pod"}

# Memory-bound processes
my_gpu_process_memory_utilization_ratio > 0.8
```

**Example:**
```prometheus
my_gpu_process_memory_utilization_ratio{pid="12345",gpu="0",...} 0.72
```

**Notes:**
- 0.0 = 0% (no memory bandwidth usage)
- 1.0 = 100% (memory bandwidth saturated)
- High values indicate memory-bound workloads

---

### my_gpu_process_memory_used_bytes

**Type:** Gauge

**Description:** GPU memory used by process.

**Unit:** Bytes

**Usage:**

```promql
# Current memory usage in GB
my_gpu_process_memory_used_bytes / (1024^3)

# Processes using > 10GB
my_gpu_process_memory_used_bytes > 10*1024*1024*1024

# Total memory used across all processes
sum(my_gpu_process_memory_used_bytes)
```

**Example:**
```prometheus
my_gpu_process_memory_used_bytes{pid="12345",gpu="0",...} 8589934592
```

**Notes:**
- Measures GPU memory (VRAM), not system RAM
- Maximum value depends on GPU model
- Includes all allocations by the process

---

### my_gpu_process_start_time_seconds

**Type:** Gauge

**Description:** Process start time in seconds since Unix epoch.

**Unit:** Seconds (Unix timestamp)

**Usage:**

```promql
# Process age in seconds
time() - my_gpu_process_start_time_seconds

# Process age in hours
(time() - my_gpu_process_start_time_seconds) / 3600

# Long-running processes (> 24 hours)
(time() - my_gpu_process_start_time_seconds) > 86400
```

**Example:**
```prometheus
my_gpu_process_start_time_seconds{pid="12345",gpu="0",...} 1699564800
```

**Notes:**
- Value is fixed for each process
- Useful for calculating process uptime
- Used to detect process restarts

---

### my_gpu_process_active

**Type:** Gauge

**Description:** Process active status.

**Unit:** Boolean (0 or 1)

**Values:**
- `1` = Process is running
- `0` = Process has exited (in retention period)

**Usage:**

```promql
# Count of active processes
sum(my_gpu_process_active)

# Count of exited processes (still in retention)
sum(my_gpu_process_active == 0)

# Active processes per namespace
sum by (namespace) (my_gpu_process_active)
```

**Example:**
```prometheus
my_gpu_process_active{pid="12345",gpu="0",...} 1
```

**Notes:**
- Changes from 1 to 0 when process exits
- Metrics retained for configurable period (default: 5 minutes)
- Allows final `rate()` calculations after process exits

---

## Advanced Queries

### Cost Attribution

```promql
# Energy cost per pod (assuming $0.10/kWh)
(increase(my_gpu_process_energy_joules{namespace="ml"}[1h]) / 3600000) * 0.10

# Total energy cost for namespace in last 24 hours
sum by (namespace) (
  (increase(my_gpu_process_energy_joules[24h]) / 3600000) * 0.10
)
```

### Power Efficiency

```promql
# Compute per Watt (higher is better)
my_gpu_process_sm_utilization_ratio / rate(my_gpu_process_energy_joules[1m])

# Energy per unit of work (assuming work metric exists)
rate(my_gpu_process_energy_joules[5m]) / rate(work_items_processed[5m])
```

### Resource Correlation

```promql
# Power vs. Utilization
rate(my_gpu_process_energy_joules[1m])
  and
my_gpu_process_sm_utilization_ratio

# Memory usage vs. Power
my_gpu_process_memory_used_bytes
  and
rate(my_gpu_process_energy_joules[1m])
```

### Alerting

```yaml
# Prometheus alert rules
groups:
  - name: gpu_process_alerts
    rules:
    - alert: HighProcessPower
      expr: rate(my_gpu_process_energy_joules[5m]) > 300
      for: 10m
      annotations:
        summary: "Process {{ $labels.pod }} using > 300W"

    - alert: LowGPUUtilization
      expr: |
        my_gpu_process_sm_utilization_ratio < 0.2
        and
        rate(my_gpu_process_energy_joules[5m]) > 100
      for: 30m
      annotations:
        summary: "Process {{ $labels.pod }} wasting power (low utilization)"

    - alert: ProcessStuckHighPower
      expr: |
        changes(my_gpu_process_energy_joules[1h]) == 0
        and
        my_gpu_process_active == 1
      annotations:
        summary: "Process {{ $labels.pod }} stopped consuming energy but still active"
```

## Grafana Dashboard Queries

### Power Over Time
```promql
rate(my_gpu_process_energy_joules{namespace="$namespace"}[5m])
```

### Energy Consumption
```promql
increase(my_gpu_process_energy_joules{pod="$pod"}[$__range])
```

### Utilization Heatmap
```promql
my_gpu_process_sm_utilization_ratio{namespace="$namespace"}
```

### Active Processes
```promql
count(my_gpu_process_active == 1) by (namespace)
```

## Retention Behavior

When a process exits:
1. `my_gpu_process_active` changes from `1` to `0`
2. Energy counter keeps its last value (does not reset)
3. All metrics retained for 5 minutes (configurable)
4. After retention period, all metrics stop being exported

This retention allows:
- Final `rate()` calculations to complete
- Historical queries during retention window
- Smooth transitions in dashboards

## Validation

To verify energy values are accurate:

```promql
# Sum of per-process energy should approximately equal GPU-level energy
sum by (gpu) (rate(my_gpu_process_energy_joules[5m]))
  vs
DCGM_FI_DEV_POWER_USAGE{gpu="0"}
```

Note: There may be small differences due to:
- System processes not tracked by exporter
- Measurement timing differences
- GPU overhead not attributed to specific processes
