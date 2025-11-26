package collector

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/vimalk78/my-gpu-exporter/pkg/config"
	"github.com/vimalk78/my-gpu-exporter/pkg/dcgm"
	"github.com/vimalk78/my-gpu-exporter/pkg/kubernetes"
	"github.com/vimalk78/my-gpu-exporter/pkg/process"
)

// ProcessMetrics contains all metrics for a single process
type ProcessMetrics struct {
	// Identity
	PID          uint
	GPU          uint
	ProcessName  string
	IsRunning    bool

	// Energy (may be measured or estimated)
	EnergyJoules    float64
	EnergyEstimated bool // True if energy is estimated (time-slicing), false if measured

	// Utilization
	SmUtilization  float64
	MemUtilization float64

	// Memory
	MemoryUsedBytes uint64

	// Timing
	StartTime time.Time
	EndTime   time.Time

	// Kubernetes labels (if available)
	PodName       string
	PodNamespace  string
	ContainerName string
	ContainerID   string
}

// Collector collects per-process GPU metrics
type Collector struct {
	config          *config.Config
	dcgmClient      *dcgm.Client
	discovery       *process.Discovery
	podMapper       *kubernetes.PodMapper
	retention       *process.RetentionManager

	mu              sync.RWMutex
	processMetrics  map[uint]*ProcessMetrics  // PID -> metrics

	// Time-slicing detection
	gpuProcessCount map[uint]int              // GPU ID -> number of active processes

	// Energy estimation timing
	lastEstimationTime map[uint]time.Time     // GPU ID -> last estimation timestamp
}

// NewCollector creates a new collector
func NewCollector(cfg *config.Config) (*Collector, error) {
	slog.Info("Initializing collector")

	// Initialize DCGM client
	dcgmClient, err := dcgm.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create DCGM client: %w", err)
	}

	// Start watching for process metrics
	if err := dcgmClient.StartWatching(); err != nil {
		return nil, fmt.Errorf("failed to start DCGM watching: %w", err)
	}

	// Initialize process discovery
	discovery, err := process.NewDiscovery()
	if err != nil {
		return nil, fmt.Errorf("failed to create process discovery: %w", err)
	}

	// Initialize Kubernetes pod mapper (if enabled)
	var podMapper *kubernetes.PodMapper
	if cfg.KubernetesEnabled {
		// Check if socket exists
		if _, err := os.Stat(cfg.PodResourcesSocket); os.IsNotExist(err) {
			slog.Warn("Kubernetes pod-resources socket not found, disabling Kubernetes integration",
				slog.String("socket", cfg.PodResourcesSocket))
		} else {
			podMapper = kubernetes.NewPodMapper(cfg.PodResourcesSocket)
			slog.Info("Kubernetes integration enabled")
		}
	}

	// Initialize retention manager
	retention := process.NewRetentionManager(cfg.MetricRetention)

	collector := &Collector{
		config:             cfg,
		dcgmClient:         dcgmClient,
		discovery:          discovery,
		podMapper:          podMapper,
		retention:          retention,
		processMetrics:     make(map[uint]*ProcessMetrics),
		gpuProcessCount:    make(map[uint]int),
		lastEstimationTime: make(map[uint]time.Time),
	}

	return collector, nil
}

// Collect performs a collection cycle
func (c *Collector) Collect() error {
	slog.Debug("Starting collection cycle")

	// Discover running processes
	processes, err := c.discovery.DiscoverProcesses()
	if err != nil {
		return fmt.Errorf("failed to discover processes: %w", err)
	}

	slog.Debug("Discovered processes", slog.Int("count", len(processes)))

	// Track which PIDs we've seen this cycle
	seenPIDs := make(map[uint]bool)

	// Collect metrics for each process
	for _, proc := range processes {
		seenPIDs[proc.PID] = true

		// Get container ID for Kubernetes filtering
		containerID, err := process.GetContainerID(proc.PID)
		if err != nil {
			slog.Debug("Failed to get container ID",
				slog.Uint64("pid", uint64(proc.PID)),
				slog.String("error", err.Error()))
			continue
		}

		// Filter: only track Kubernetes pods
		if containerID == "" {
			// Not a containerized process - skip
			slog.Debug("Skipping non-containerized process",
				slog.Uint64("pid", uint64(proc.PID)))
			continue
		}

		// Get Kubernetes pod info (if enabled)
		var podInfo *kubernetes.PodInfo
		if c.podMapper != nil {
			podInfo, err = c.podMapper.GetPodInfo(containerID)
			if err != nil {
				slog.Debug("Failed to get pod info by container ID",
					slog.Uint64("pid", uint64(proc.PID)),
					slog.String("container_id", containerID),
					slog.String("error", err.Error()))
			}

			// Fallback: try lookup by pod UID from cgroup
			if podInfo == nil {
				podUID, _ := process.GetPodUID(proc.PID)
				if podUID != "" {
					podInfo, _ = c.podMapper.GetPodInfoByUID(podUID)
				}
			}

			if podInfo == nil {
				slog.Debug("Pod info not available, exporting with empty pod labels",
					slog.Uint64("pid", uint64(proc.PID)),
					slog.String("container_id", containerID))
			}
		}

		// Get DCGM metrics for this process
		metrics, err := c.dcgmClient.GetProcessMetrics(proc.PID)
		if err != nil {
			slog.Warn("Failed to get DCGM metrics",
				slog.Uint64("pid", uint64(proc.PID)),
				slog.String("error", err.Error()))
			continue
		}

		if metrics == nil {
			// No DCGM data available yet
			continue
		}

		// Fallback to NVML memory if DCGM doesn't provide it
		// DCGM's Memory.GlobalUsed is not supported on all GPU types (e.g., Tesla T4)
		memoryUsed := metrics.MemoryUsedBytes
		if memoryUsed == 0 && proc.MemoryUsed > 0 {
			memoryUsed = proc.MemoryUsed
			slog.Debug("Using NVML memory fallback (DCGM returned 0)",
				slog.Uint64("pid", uint64(proc.PID)),
				slog.Uint64("nvml_memory_bytes", proc.MemoryUsed))
		}

		// Build process metrics
		pm := &ProcessMetrics{
			PID:             proc.PID,
			GPU:             metrics.GPU,
			ProcessName:     metrics.ProcessName,
			IsRunning:       metrics.IsRunning,
			EnergyJoules:    metrics.EnergyConsumed,
			SmUtilization:   metrics.SmUtilization,
			MemUtilization:  metrics.MemUtilization,
			MemoryUsedBytes: memoryUsed,
			StartTime:       metrics.StartTime,
			EndTime:         metrics.EndTime,
			ContainerID:     containerID,
		}

		// Add Kubernetes labels
		if podInfo != nil {
			pm.PodName = podInfo.PodName
			pm.PodNamespace = podInfo.PodNamespace
			pm.ContainerName = podInfo.ContainerName
		}

		// Store metrics - preserve accumulated energy if estimation was active
		c.mu.Lock()
		if existingPM, exists := c.processMetrics[proc.PID]; exists && existingPM.EnergyEstimated {
			// Preserve accumulated energy from estimation
			pm.EnergyJoules = existingPM.EnergyJoules
			pm.EnergyEstimated = true
		}
		c.processMetrics[proc.PID] = pm
		c.mu.Unlock()

		slog.Debug("Collected metrics for process",
			slog.Uint64("pid", uint64(proc.PID)),
			slog.Float64("energy_joules", pm.EnergyJoules),
			slog.String("pod", pm.PodName))
	}

	// Check for exited processes
	c.mu.Lock()
	for pid, pm := range c.processMetrics {
		if !seenPIDs[pid] && !c.retention.IsExited(pid) {
			// Process no longer running - mark as exited
			pm.IsRunning = false
			c.retention.MarkExited(pid)
			slog.Info("Process exited",
				slog.Uint64("pid", uint64(pid)),
				slog.String("pod", pm.PodName))
		}
	}
	c.mu.Unlock()

	// Remove metrics for expired processes (must be done BEFORE CleanupExpired)
	c.mu.Lock()
	for _, pid := range c.retention.GetExitedProcesses() {
		if !c.retention.ShouldRetain(pid) {
			delete(c.processMetrics, pid)
			slog.Debug("Removed metrics for expired process",
				slog.Uint64("pid", uint64(pid)))
		}
	}
	c.mu.Unlock()

	// Clean up expired processes from retention manager
	c.retention.CleanupExpired()

	// Detect and validate time-slicing
	c.detectAndValidateTimeSlicing()

	return nil
}

// detectAndValidateTimeSlicing detects GPU time-slicing and applies estimation if needed
func (c *Collector) detectAndValidateTimeSlicing() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Count active processes per GPU
	gpuProcesses := make(map[uint][]*ProcessMetrics)

	for _, pm := range c.processMetrics {
		if pm.IsRunning {
			gpuProcesses[pm.GPU] = append(gpuProcesses[pm.GPU], pm)
		}
	}

	// Check each GPU for time-slicing
	for gpuID, processes := range gpuProcesses {
		processCount := len(processes)

		// Update process count tracking
		c.gpuProcessCount[gpuID] = processCount

		// Single process - no time-slicing, use DCGM values directly
		if processCount == 1 {
			slog.Debug("Single process on GPU, using DCGM measured energy",
				slog.Uint64("gpu", uint64(gpuID)))
			continue
		}

		// Multiple processes detected - time-slicing scenario
		// Always use estimation for time-slicing
		slog.Info("Time-slicing detected: using SM-based energy estimation",
			slog.Uint64("gpu", uint64(gpuID)),
			slog.Int("process_count", processCount))

		if c.config.EnableEnergyEstimation {
			err := c.applyEnergyEstimation(gpuID, processes)
			if err != nil {
				slog.Warn("Failed to apply energy estimation",
					slog.Uint64("gpu", uint64(gpuID)),
					slog.String("error", err.Error()))
			}
		} else {
			slog.Warn("Time-slicing detected but estimation is disabled",
				slog.Uint64("gpu", uint64(gpuID)),
				slog.Int("process_count", processCount),
				slog.String("hint", "Enable --enable-energy-estimation for accurate per-process attribution"))
		}
	}
}

// applyEnergyEstimation estimates per-process energy based on SM utilization
func (c *Collector) applyEnergyEstimation(gpuID uint, processes []*ProcessMetrics) error {
	// Calculate total SM utilization across all processes
	var totalSMUtil float64
	for _, pm := range processes {
		totalSMUtil += pm.SmUtilization
	}

	if totalSMUtil == 0 {
		slog.Debug("No SM utilization detected, cannot estimate energy",
			slog.Uint64("gpu", uint64(gpuID)))
		return nil
	}

	// Get GPU-level power usage
	gpuPower, err := c.dcgmClient.GetGPUPowerUsage(gpuID)
	if err != nil {
		return fmt.Errorf("failed to get GPU power: %w", err)
	}

	// Calculate actual elapsed time since last estimation
	now := time.Now()
	var intervalSeconds float64
	if lastTime, exists := c.lastEstimationTime[gpuID]; exists {
		intervalSeconds = now.Sub(lastTime).Seconds()
	} else {
		// First estimation - use configured interval as fallback
		intervalSeconds = c.config.DCGMUpdateFrequency.Seconds()
	}
	c.lastEstimationTime[gpuID] = now

	// Subtract idle power to get active power only
	activePower := gpuPower - c.config.GPUIdlePower
	if activePower < 0 {
		activePower = 0 // Can't be negative
	}

	slog.Debug("GPU power for estimation",
		slog.Uint64("gpu", uint64(gpuID)),
		slog.Float64("total_power_watts", gpuPower),
		slog.Float64("idle_power_watts", c.config.GPUIdlePower),
		slog.Float64("active_power_watts", activePower),
		slog.Float64("total_sm_util", totalSMUtil),
		slog.Float64("interval_seconds", intervalSeconds))

	// Calculate total GPU energy for this interval (using active power only)
	gpuEnergyJoules := activePower * intervalSeconds

	// Distribute energy proportionally based on SM utilization
	for _, pm := range processes {
		// Proportional attribution: process_energy = gpu_energy * (process_sm_util / total_sm_util)
		proportion := pm.SmUtilization / totalSMUtil
		estimatedEnergyInterval := gpuEnergyJoules * proportion

		// Accumulate the energy (counter behavior)
		// Note: This adds the estimated energy for this interval to the cumulative total
		previousEnergy := pm.EnergyJoules
		pm.EnergyJoules += estimatedEnergyInterval
		pm.EnergyEstimated = true

		slog.Debug("Applied energy estimation",
			slog.Uint64("pid", uint64(pm.PID)),
			slog.String("pod", pm.PodName),
			slog.Float64("sm_util", pm.SmUtilization),
			slog.Float64("proportion", proportion),
			slog.Float64("interval_energy_J", estimatedEnergyInterval),
			slog.Float64("previous_total_J", previousEnergy),
			slog.Float64("new_total_J", pm.EnergyJoules))
	}

	return nil
}

// GetMetrics returns current metrics snapshot
func (c *Collector) GetMetrics() map[uint]*ProcessMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Return a copy to avoid concurrent access issues
	metrics := make(map[uint]*ProcessMetrics, len(c.processMetrics))
	for pid, pm := range c.processMetrics {
		pmCopy := *pm
		metrics[pid] = &pmCopy
	}

	return metrics
}

// Shutdown cleans up resources
func (c *Collector) Shutdown() error {
	slog.Info("Shutting down collector")

	if c.dcgmClient != nil {
		c.dcgmClient.Shutdown()
	}

	if c.discovery != nil {
		c.discovery.Shutdown()
	}

	return nil
}
