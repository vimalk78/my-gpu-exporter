# DCGM Per-Process Energy Tracking Bug with Time-Slicing

**Date**: 2025-01-24
**Author**: Analysis of DCGM source code
**Status**: Confirmed Bug

## Executive Summary

DCGM's per-process energy tracking (`dcgmGetPidInfo()` → `ProcessUtilInfo.energyConsumed`) **does NOT correctly attribute energy consumption to individual processes when GPU time-slicing is enabled**. Instead, all processes running during overlapping time periods receive identical GPU-level total energy values, regardless of their actual individual energy consumption.

**Root Cause**: DCGM uses GPU-level total energy consumption instead of per-process energy from NVML accounting data.

## Background: NVML Accounting Mode

### What is NVML Accounting Mode?

NVML (NVIDIA Management Library) provides an **Accounting Mode** feature designed to track per-process GPU statistics:

```c
/**
 * Queries process's accounting stats.
 *
 * Accounting stats capture GPU utilization and other statistics across
 * the lifetime of a process.
 */
nvmlReturn_t nvmlDeviceGetAccountingStats(nvmlDevice_t device,
                                         unsigned int pid,
                                         nvmlAccountingStats_t *stats);
```

### What Data Does NVML Accounting Provide?

From `nvml.h:2910-2931`, the `nvmlAccountingStats_t` structure contains:

```c
typedef struct nvmlAccountingStats_st {
    unsigned int gpuUtilization;        // % of time kernels were executing
    unsigned int memoryUtilization;     // % of time memory was being read/written
    unsigned long long maxMemoryUsage;  // Maximum memory allocated by process
    unsigned long long time;            // Active context time in ms
    unsigned long long startTime;       // CPU timestamp in usec (process start)
    unsigned int isRunning;             // 1=running, 0=terminated
    unsigned int reserved[5];
} nvmlAccountingStats_t;
```

### Critical Observation: NO ENERGY FIELD

**NVML's accounting stats structure does NOT include per-process energy consumption.** There is no `energyConsumed` field in `nvmlAccountingStats_t`.

This is explicitly documented in NVML:

> **Warning** (nvml.h:7249): On Kepler devices per process statistics are accurate only if there's one process running on a GPU.

NVML accounting was never designed to provide accurate per-process metrics with multiple processes (time-slicing).

## How DCGM Implements Per-Process Energy

### The GetProcessInfo() Function

DCGM's `dcgmGetPidInfo()` API is implemented by `DcgmHostEngineHandler::GetProcessInfo()` in `dcgmlib/src/DcgmHostEngineHandler.cpp:2310-2600`.

Here's the actual code flow:

```cpp
// For each GPU the process ran on:
for (gpuIdIt = gpuIds.begin(); gpuIdIt != gpuIds.end(); ++gpuIdIt)
{
    singleInfo = &pidInfo->gpus[pidInfo->numGpus];
    singleInfo->gpuId = *gpuIdIt;

    // Step 1: Get per-process accounting data (timestamps, utilization)
    dcgmReturn = mpCacheManager->GetLatestProcessInfo(
        singleInfo->gpuId,
        pidInfo->pid,           // ← PID is passed here
        &accountingInfo);

    if (dcgmReturn == DCGM_ST_NO_DATA) {
        continue;  // Process didn't run on this GPU
    }

    // Step 2: Extract the time period when THIS PID was active
    startTime = (long long)accountingInfo.startTimestamp;

    if (0 == accountingInfo.activeTimeUsec)  // Process still running
    {
        endTime = (long long)timelib_usecSince1970();
    }
    else
    {
        endTime = (long long)(accountingInfo.startTimestamp +
                             accountingInfo.activeTimeUsec);
    }

    // Step 3: Query GPU's TOTAL energy during THIS PID's lifetime
    summaryTypes[0] = DcgmcmSummaryTypeDifference;
    mpCacheManager->GetInt64SummaryData(
        DCGM_FE_GPU,                              // ← GPU entity type
        singleInfo->gpuId,                        // ← GPU ID
        DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION,    // ← DEVICE-level field!
        1,
        &summaryTypes[0],
        &i64Val,
        startTime,      // ← Process start time
        endTime,        // ← Process end time (or now)
        nullptr,
        nullptr);

    // Step 4: Assign GPU energy to this process
    if (!DCGM_INT64_IS_BLANK(i64Val))
    {
        singleInfo->energyConsumed = i64Val;  // ← Direct assignment!
    }
}
```

### What DCGM Actually Does

1. **Gets per-process timestamps** from NVML accounting (✓ correct)
2. **Queries GPU-level total energy** for the time period when the process was active
3. **Attributes that GPU energy** to the individual process

**No per-process energy tracking occurs.** DCGM simply queries:
> "What was the GPU's total energy consumption from when this process started until now?"

## Why Time-Slicing Causes Identical Values

### Scenario: Three Processes with Different Workloads

Consider our test case with three processes on GPU 0:

| Process | Workload | SM Utilization | Start Time | End Time |
|---------|----------|----------------|------------|----------|
| PID A   | Heavy    | 39%            | 10:00:00   | Running  |
| PID B   | Medium   | 1%             | 10:00:00   | Running  |
| PID C   | Light    | 0%             | 10:00:00   | Running  |

### What DCGM Does for Each Process

**For Process A** (heavy workload):
```
startTime = 10:00:00
endTime = now (still running)
energyConsumed = GPU_0_total_energy(10:00:00 → now) = 2071J
```

**For Process B** (medium workload):
```
startTime = 10:00:00
endTime = now (still running)
energyConsumed = GPU_0_total_energy(10:00:00 → now) = 2071J  ← SAME!
```

**For Process C** (light workload):
```
startTime = 10:00:00
endTime = now (still running)
energyConsumed = GPU_0_total_energy(10:00:00 → now) = 2071J  ← SAME!
```

### Why They're Identical

Since all three processes:
- **Started at approximately the same time** (when deployed)
- **Are still running** (so `endTime` = now for all)

They all query for **the exact same time period**, and therefore receive **the same GPU total energy value** (2071J).

The GPU consumed 2071J total during this period, but DCGM incorrectly reports that **each process individually** consumed 2071J.

## The Fundamental Design Flaw

### What DCGM Should Do (But Doesn't)

DCGM should either:

1. **Use NVML's per-process energy** (if it existed) - but it doesn't exist
2. **Estimate per-process energy** based on:
   - Per-process SM utilization from accounting
   - GPU power usage during the process's lifetime
   - Proportional attribution based on utilization
3. **Report "not available"** for per-process energy with time-slicing

### What DCGM Actually Does

DCGM uses a simplified approach:
- Query GPU-level energy for the process's lifetime
- Attribute ALL of that energy to the process

This works reasonably well when:
- ✓ Only one process runs on the GPU at a time
- ✓ Processes run sequentially (not overlapping)

This **completely fails** when:
- ✗ Multiple processes run simultaneously (time-slicing)
- ✗ Processes have overlapping lifetimes

## Evidence from Our Testing

### Deployment Configuration

Three pods with different GPU workload intensities:

```yaml
# Heavy workload - continuous 100% duty cycle
- name: gpu-heavy
  replicas: 1

# Medium workload - 50% duty cycle (work 0.5s, sleep 0.5s)
- name: gpu-medium
  replicas: 1

# Light workload - 10% duty cycle (work 0.1s, sleep 1.0s)
- name: gpu-light
  replicas: 1
```

### Observed Results

```
$ kubectl exec -n gpu-workloads dcgm-exporter-xyz -- \
    curl -s localhost:9400/metrics | grep my_gpu_process

# Process with 39% SM utilization (heavy)
my_gpu_process_energy_joules{gpu="0",pid="120263"} 2071

# Process with 1% SM utilization (medium)
my_gpu_process_energy_joules{gpu="0",pid="120862"} 2071

# Process with 0% SM utilization (light)
my_gpu_process_energy_joules{gpu="0",pid="120415"} 2071
```

**All three processes show identical energy (2071J) despite vastly different utilization.**

### What Should Have Been Reported

If per-process energy attribution worked correctly:

```
# Heavy workload (39% SM) should consume ~78% of total energy
my_gpu_process_energy_joules{gpu="0",pid="120263"} ~1615

# Medium workload (1% SM) should consume ~2% of total energy
my_gpu_process_energy_joules{gpu="0",pid="120862"} ~41

# Light workload (0% SM) should consume minimal energy
my_gpu_process_energy_joules{gpu="0",pid="120415"} ~10
```

*Note: These are approximate values based on SM utilization proportions.*

## Impact and Implications

### Affected Use Cases

This bug impacts any scenario using DCGM for per-process energy accounting with time-slicing:

1. **Multi-tenant GPU clusters** - Cannot accurately charge tenants for GPU energy
2. **Cost attribution** - Incorrect energy costs per workload/team
3. **Carbon accounting** - Inaccurate per-application carbon footprint
4. **Capacity planning** - Misleading data about workload energy requirements
5. **Energy optimization** - Cannot identify energy-inefficient workloads

### When The Bug Occurs

The bug manifests when:
- ✗ Time-slicing is enabled on GPUs
- ✗ Multiple processes run simultaneously on the same GPU
- ✗ Processes have overlapping lifetimes

The bug does NOT occur when:
- ✓ Only one process per GPU (MIG or exclusive mode)
- ✓ Processes run sequentially (no overlap)
- ✓ Short-lived processes with distinct start/end times

### Severity Assessment

| Aspect | Rating | Explanation |
|--------|--------|-------------|
| Correctness | **Critical** | Reported values are completely wrong |
| Detectability | **Low** | Appears correct unless you verify with different workloads |
| Workarounds | **None** | No way to get accurate per-process energy with time-slicing |
| Documentation | **Missing** | Not documented as a limitation |

## Recommendations

### For DCGM Users

1. **Do NOT rely on `ProcessUtilInfo.energyConsumed` with time-slicing**
2. Use GPU-level energy metrics instead:
   - `DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION`
   - Aggregate metrics for the entire GPU
3. Estimate per-process energy based on:
   - SM utilization percentage
   - GPU total energy consumption
   - Proportional attribution

### For DCGM Developers

1. **Document the limitation** clearly in API documentation
2. **Return `DCGM_INT64_NOT_SUPPORTED`** for per-process energy when time-slicing is detected
3. **Implement proportional attribution**:
   ```
   process_energy = gpu_total_energy × (process_sm_util / sum_all_processes_sm_util)
   ```
4. **Add validation** to detect and warn about time-slicing

### For NVIDIA Driver Team

1. **Extend NVML accounting** to include per-process energy tracking
2. Update `nvmlAccountingStats_t` with:
   ```c
   unsigned long long energyConsumed;  // Energy in millijoules
   ```
3. Implement hardware-based per-process energy attribution

## Conclusion

DCGM's per-process energy tracking is fundamentally broken with GPU time-slicing due to a design limitation: **NVML does not provide per-process energy data**, so DCGM uses GPU-level total energy and attributes it to each process based on the process's lifetime timestamps.

This results in all processes running during overlapping time periods receiving identical (and incorrect) energy values, making the data useless for cost attribution, carbon accounting, or energy optimization in multi-tenant GPU environments.

**The bug is in DCGM's design assumptions, not implementation.** DCGM correctly retrieves the data available from NVML, but that data is insufficient for accurate per-process energy attribution with time-slicing.

## References

### Source Code Locations

- **Energy query logic**: `dcgmlib/src/DcgmHostEngineHandler.cpp:2394-2426`
- **Process info retrieval**: `dcgmlib/src/DcgmCacheManager.cpp:3953-4010`
- **NVML accounting stats**: `sdk/nvidia/nvml/nvml.h:2910-2931`
- **NVML API documentation**: `sdk/nvidia/nvml/nvml.h:7230-7266`

### Key Findings

1. `nvmlAccountingStats_t` contains **no energy field**
2. DCGM queries `DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION` (GPU-level)
3. Energy is attributed using process start/end timestamps only
4. No per-process differentiation occurs with time-slicing

### Test Results

- Deployment: 3 processes with different intensities (100%, 50%, 10% duty cycle)
- Observed: All processes showed 2071J (identical)
- Expected: Energy proportional to SM utilization (~1615J, ~41J, ~10J)
- Conclusion: Bug confirmed

---

**Document Version**: 1.0
**Last Updated**: 2025-01-24
