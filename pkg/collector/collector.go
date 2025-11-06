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

	// ACTUAL MEASURED ENERGY (not estimated)
	EnergyJoules float64

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
		config:         cfg,
		dcgmClient:     dcgmClient,
		discovery:      discovery,
		podMapper:      podMapper,
		retention:      retention,
		processMetrics: make(map[uint]*ProcessMetrics),
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
				slog.Warn("Failed to get pod info",
					slog.Uint64("pid", uint64(proc.PID)),
					slog.String("container_id", containerID),
					slog.String("error", err.Error()))
			}

			if podInfo == nil {
				// Pod info not available - export metrics with empty pod labels
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

		// Store metrics
		c.mu.Lock()
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
