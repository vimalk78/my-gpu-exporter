package process

import (
	"fmt"
	"log/slog"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// Discovery handles finding GPU processes
type Discovery struct {
	initialized bool
}

// ProcessInfo contains basic process information
type ProcessInfo struct {
	PID         uint
	GPU         uint
	MemoryUsed  uint64
}

// NewDiscovery creates a new process discovery instance
func NewDiscovery() (*Discovery, error) {
	slog.Info("Initializing NVML for process discovery")

	// Initialize NVML
	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to initialize NVML: %v", nvml.ErrorString(ret))
	}

	return &Discovery{
		initialized: true,
	}, nil
}

// DiscoverProcesses finds all GPU processes across all GPUs
func (d *Discovery) DiscoverProcesses() ([]ProcessInfo, error) {
	if !d.initialized {
		return nil, fmt.Errorf("discovery not initialized")
	}

	// Get GPU count
	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to get device count: %v", nvml.ErrorString(ret))
	}

	slog.Debug("Scanning for GPU processes", slog.Int("gpu_count", count))

	var allProcesses []ProcessInfo

	// Scan each GPU
	for i := 0; i < count; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			slog.Warn("Failed to get device handle",
				slog.Int("gpu", i),
				slog.String("error", nvml.ErrorString(ret)))
			continue
		}

		// Get compute processes (excludes graphics processes)
		processes, ret := device.GetComputeRunningProcesses()
		if ret != nvml.SUCCESS {
			slog.Warn("Failed to get running processes",
				slog.Int("gpu", i),
				slog.String("error", nvml.ErrorString(ret)))
			continue
		}

		// Add to results
		for _, proc := range processes {
			allProcesses = append(allProcesses, ProcessInfo{
				PID:        uint(proc.Pid),
				GPU:        uint(i),
				MemoryUsed: proc.UsedGpuMemory,
			})
		}

		slog.Debug("Found GPU processes",
			slog.Int("gpu", i),
			slog.Int("process_count", len(processes)))
	}

	slog.Debug("Total GPU processes discovered", slog.Int("total", len(allProcesses)))

	return allProcesses, nil
}

// Shutdown cleans up NVML resources
func (d *Discovery) Shutdown() error {
	if !d.initialized {
		return nil
	}

	slog.Info("Shutting down NVML")
	ret := nvml.Shutdown()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("failed to shutdown NVML: %v", nvml.ErrorString(ret))
	}

	d.initialized = false
	return nil
}
