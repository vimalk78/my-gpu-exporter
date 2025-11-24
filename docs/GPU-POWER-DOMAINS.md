# GPU Power Domains and Zones

**Date**: 2025-01-24
**Question**: Like RAPL's core/DRAM zones, are there such zones in GPU power?

## TL;DR: Similar Domain-Level Breakdown

**Short Answer**: Yes, at a similar level to RAPL.

GPUs provide power measurement at **3 scopes**:
1. **GPU** - GPU chip only (all SMs, caches, graphics combined)
2. **Memory** - HBM/GDDR memory power (limited availability)
3. **Module** - Total module (GPU + CPU on Grace Hopper systems)

RAPL provides **4-5 power domains**:
1. **PP0** - Core domain (all cores combined)
2. **PP1** - Uncore/iGPU domain
3. **DRAM** - Memory power
4. **Platform** - SoC-wide (optional)

**Key similarity**: Both provide **domain-level aggregate** power, not per-component breakdown.

## Comparison: RAPL vs GPU Power Domains

### Intel RAPL (Domain-Level Breakdown)

RAPL provides **4-5 power domains** on modern CPUs:

```
Package Power (PP0 + PP1 + DRAM + Platform)
├── PP0 (Core Domain - ALL cores combined)
│   └── Cannot distinguish individual cores
├── PP1 (Uncore/GPU Domain)
│   └── Integrated GPU (if present) + uncore
├── DRAM
│   └── Memory controller + DIMM power
└── Platform (optional)
    └── SoC package + I/O
```

**RAPL Features**:
- ✓ Separate core domain (PP0) - **but ALL cores aggregated, not per-core**
- ✓ Separate DRAM power
- ✓ Uncore/GPU domain (PP1)
- ✓ Platform-wide measurement (optional)
- ✓ High sampling rate (~1ms)
- ✗ **No per-core breakdown**

### NVIDIA GPU Power Scopes (Coarse Breakdown)

From NVML API (`nvml.h:1449-1451`):

```c
#define NVML_POWER_SCOPE_GPU     0U    // Targets only GPU
#define NVML_POWER_SCOPE_MODULE  1U    // Targets the whole module
#define NVML_POWER_SCOPE_MEMORY  2U    // Targets the GPU Memory
```

**GPU Power Hierarchy**:

```
Module Power (GPU + optional CPU)
├── GPU Power
│   ├── SM Array (compute cores)
│   ├── Graphics Engine
│   ├── Tensor Cores
│   ├── RT Cores
│   ├── Video Encode/Decode
│   ├── L2 Cache
│   ├── Memory Controllers
│   ├── PCIe/NVLink
│   └── Voltage Regulators
└── Memory Power (HBM/GDDR)
    └── Memory stacks/chips
```

**GPU Features**:
- ✓ Total GPU power (most common)
- ✓ Memory power (HBM/GDDR) - **on supported GPUs**
- ✓ Module power (GPU+CPU) - **Grace Hopper only**
- ✗ No per-SM power
- ✗ No per-component breakdown
- ✗ No per-process hardware metering

## Available Power Measurements in NVML/DCGM

### NVML Power APIs

From the [NVML API documentation](https://docs.nvidia.com/deploy/nvml-api/group__nvmlDeviceEnumvs.html):

| API | Scope | Description | Availability |
|-----|-------|-------------|--------------|
| `nvmlDeviceGetPowerUsage()` | GPU | Current GPU power (mW) | All GPUs |
| `nvmlDeviceGetTotalEnergyConsumption()` | GPU | Total energy since driver load (mJ) | Volta+ |
| `nvmlDeviceGetPowerManagementLimit()` | GPU/Module | Power limit configuration | All GPUs |
| Power with scope parameter | GPU/Memory/Module | Scoped power measurement | Newer GPUs |

### DCGM Power Fields

From `dcgm_fields.h`:

```c
// GPU Power (most common)
DCGM_FI_DEV_POWER_USAGE              // Current power in watts
DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION // Cumulative energy in millijoules

// Power Limits
DCGM_FI_DEV_POWER_MGMT_LIMIT        // Configured power limit
DCGM_FI_DEV_POWER_MGMT_LIMIT_MIN    // Minimum allowed limit
DCGM_FI_DEV_POWER_MGMT_LIMIT_MAX    // Maximum allowed limit
DCGM_FI_DEV_POWER_MGMT_LIMIT_DEF    // Default power limit

// Newer Fields (Ampere+)
DCGM_FI_DEV_POWER_AVERAGE           // Power averaged over 1 sec
DCGM_FI_DEV_POWER_INSTANT           // Instantaneous power
DCGM_FI_DEV_ENERGY                  // Same as TOTAL_ENERGY_CONSUMPTION

// Power violation tracking
DCGM_FI_DEV_POWER_VIOLATION         // Time spent power-throttled
```

**Key Observation**: All DCGM power fields are **GPU-wide**. No per-component fields.

## What Each Scope Measures

### 1. GPU Scope (Most Common)

**What it includes**:
- SM (Streaming Multiprocessor) array power
- Graphics engines (if active)
- Tensor Cores / RT Cores
- Video encoder/decoder engines
- L1/L2 caches
- Memory controllers
- On-chip interconnects
- Voltage regulators for core domain
- PCIe/NVLink interfaces

**What it excludes**:
- ✗ HBM/GDDR memory chip power
- ✗ PCIe slot power (motherboard side)
- ✗ Cooling fans (if present)

**Typical values**:
- Idle: 20-50W
- Typical workload: 150-250W
- Peak: 250-700W (depending on GPU model)

**Example**:
```bash
$ nvidia-smi --query-gpu=power.draw --format=csv
power.draw [W]
235.67
```

### 2. Memory Scope (Limited Availability)

**What it includes**:
- HBM/GDDR memory chip power
- Memory PHY (physical interface) power
- Memory I/O power

**What it excludes**:
- ✗ Memory controllers (counted in GPU scope)
- ✗ L1/L2 caches (counted in GPU scope)

**Availability**:
- ✓ HBM-based GPUs (V100, A100, H100, etc.)
- ✗ Most consumer GPUs (no separate memory power)
- ✗ Older datacenter GPUs

**Typical values**:
- HBM2: 30-50W
- HBM2e: 35-60W
- HBM3: 40-70W

**Important**: Not widely exposed in DCGM fields. May require direct NVML calls with scope parameter.

### 3. Module Scope (Grace Hopper Only)

**What it includes**:
- GPU power (everything from GPU scope)
- Memory power (HBM)
- NVIDIA Grace CPU power
- NVLink-C2C interconnect
- Shared voltage regulators

**Availability**:
- ✓ Grace Hopper (H100 NVL, GH200)
- ✗ All other GPUs

**Purpose**: For integrated CPU+GPU modules to measure total system power.

**Example hierarchy**:
```
Grace Hopper Module (900W TDP)
├── Grace CPU (500W max)
├── Hopper GPU (700W max)
│   └── HBM3 Memory
└── C2C Interconnect
```

From NVML comment (`nvml.h:566`):
> "To represent module power samples for total module starting Grace Hopper"

## Why No Detailed Breakdown?

### Technical Reasons

1. **Shared Power Rails**: GPU components often share voltage rails, making per-component measurement difficult

2. **Dynamic Voltage/Frequency**: Components scale voltage/frequency independently, complicating attribution

3. **Hardware Complexity**: Would require:
   - Per-component power sensors
   - Additional die area
   - More expensive power delivery
   - Complex firmware

4. **Privacy/Security**: Detailed power traces could leak information about:
   - What algorithms are running
   - Data being processed
   - Cryptographic keys (power analysis attacks)

### Business Reasons

1. **Limited Use Cases**: Most users care about total power, not breakdown
2. **Competitive Information**: Component power reveals architecture details
3. **Thermal Management**: GPU manages power holistically, not per-component

## What About Per-Process Power?

**Neither RAPL nor NVML provide true per-process power**:

### RAPL Limitations
- ❌ No per-process core power
- ❌ No per-core power (PP0 = all cores combined)
- ❌ Core domain power shared across all processes/cores
- ✓ Can estimate: `Process_Power ≈ PP0_Power × (Process_CPU_Time / Total_CPU_Time)`

### NVML Limitations
- ❌ No per-process GPU power (as we documented)
- ❌ No per-process SM power
- ❌ No hardware per-process metering
- ✓ Can estimate: `Process_Power ≈ GPU_Power × (Process_SM_Util / Total_SM_Util)`

**Both require estimation for per-process attribution.**

## Practical Implications

### What You CAN Measure

For typical datacenter GPUs (V100, A100, H100):

```go
// Available measurements
totalGPUPower := dcgm.GetField(DCGM_FI_DEV_POWER_USAGE)        // ✓ Accurate
totalEnergy := dcgm.GetField(DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION) // ✓ Accurate
powerLimit := dcgm.GetField(DCGM_FI_DEV_POWER_MGMT_LIMIT)      // ✓ Accurate

// Per-process (requires estimation)
processSMUtil := dcgm.GetProcessSMUtilization(pid)              // ✓ Accurate
processMemUtil := dcgm.GetProcessMemUtilization(pid)            // ✓ Accurate
processEnergyEstimate := EstimateProcessEnergy(...)             // ⚠ Estimated
```

### What You CANNOT Measure

```go
// Not available in NVML/DCGM
smArrayPower := ???           // ❌ No API
memorySysPower := ???         // ❌ Not exposed (exists but not in DCGM)
tensorCorePower := ???        // ❌ No API
nvlinkPower := ???            // ❌ No API
perProcessHWPower := ???      // ❌ No hardware support
```

### Workarounds for Component Power

If you need component-level estimates:

```python
# Rough estimation based on activity
def estimate_component_power(gpu_total_power, metrics):
    """
    Rough breakdown estimation (NOT from hardware).
    """
    # These are approximations, not measurements!
    idle_power = 30  # W
    dynamic_power = gpu_total_power - idle_power

    # Proportional attribution (very rough)
    sm_power = dynamic_power * (metrics['sm_util'] / 100) * 0.6
    mem_power = dynamic_power * (metrics['mem_util'] / 100) * 0.3
    other_power = dynamic_power * 0.1 + idle_power

    return {
        'sm_estimate': sm_power,
        'memory_estimate': mem_power,
        'other_estimate': other_power,
        'warning': 'These are estimates, not hardware measurements'
    }
```

**Warning**: This is pure estimation, not hardware data!

## Recommendations

### For Power Monitoring

1. **Use GPU-scope power** for most use cases:
   ```bash
   nvidia-smi --query-gpu=power.draw,power.limit --format=csv
   ```

2. **Use DCGM for historical data**:
   ```go
   DCGM_FI_DEV_POWER_USAGE              // Current power
   DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION // Energy counter
   ```

3. **For per-process**: Use our SM+memory estimation approach (see `PER-PROCESS-POWER-ESTIMATION.md`)

### For Research/Detailed Analysis

If you need component-level power:

1. **External power measurement**:
   - Use PCIe slot power meters
   - Measure at PSU rails
   - Use whole-system power meters

2. **GPU-specific tools**:
   - NVIDIA Nsight Systems (profiling)
   - CUPTI (CUDA Profiling Tools Interface)
   - May provide more detailed metrics

3. **Accept GPU-level granularity**:
   - Most use cases don't need finer breakdown
   - Focus on optimizing total GPU power

## Comparison Table

| Feature | Intel RAPL | NVIDIA GPU |
|---------|-----------|------------|
| **Total Package Power** | ✓ Yes | ✓ Yes (GPU) |
| **Core/SM Power** | ✓ Domain (all cores) | ✓ Total (all SMs) |
| **Per-Core/Per-SM** | ✗ No | ✗ No |
| **Memory Power** | ✓ DRAM separate | ⚠ Limited (HBM only) |
| **Uncore/Graphics** | ✓ PP1 domain | ✗ No (included in GPU) |
| **Per-Process HW** | ✗ No | ✗ No |
| **Sampling Rate** | ~1ms | ~1-10ms |
| **Power Domains** | 4-5 domains | 1-3 scopes |
| **Availability** | Most Intel CPUs | GPU-dependent |

## Conclusion

**GPU power measurement is similar to RAPL - both provide domain-level aggregates:**

### RAPL (Intel CPUs)
- ✓ **4-5 power domains** (PP0, PP1, DRAM, Platform)
- ✗ **No per-core power** (PP0 = all cores combined)
- ✗ **No per-process hardware metering**
- ✓ Requires estimation for per-process/per-core attribution

### GPU (NVIDIA)
- ✓ **1-3 power scopes** (GPU, Memory, Module)
- ✗ **No per-SM power** (GPU = all SMs combined)
- ✗ **No per-process hardware metering**
- ✓ Requires estimation for per-process/per-SM attribution

**Key Insight**: Both RAPL and GPU provide **aggregate domain power** that must be **estimated** for finer-grained attribution. Neither provides true per-core/per-SM or per-process hardware metering.

For **per-process power attribution** with time-slicing, we must estimate using utilization metrics (see our other documentation).

## References

- [NVML API Reference](https://docs.nvidia.com/deploy/nvml-api/group__nvmlDeviceEnumvs.html)
- [NVIDIA Management Library (NVML)](https://developer.nvidia.com/management-library-nvml)
- NVML Header: `sdk/nvidia/nvml/nvml.h:1449-1451` (Power scope definitions)
- DCGM Fields: `dcgmlib/dcgm_fields.h` (Power field definitions)

---

**Document Version**: 1.1
**Last Updated**: 2025-01-24
**Changelog**: v1.1 - Corrected RAPL description: PP0 is all cores combined, not per-core
