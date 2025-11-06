package dcgm

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/NVIDIA/go-dcgm/pkg/dcgm"
)

// Client wraps DCGM functionality for per-process energy tracking
type Client struct {
	groupHandle dcgm.GroupHandle
	initialized bool
}

// ProcessMetrics contains per-process GPU metrics from DCGM
// IMPORTANT: EnergyConsumed is ACTUAL measured energy from GPU hardware, NOT estimated
type ProcessMetrics struct {
	PID              uint
	GPU              uint
	ProcessName      string

	// Energy - ACTUAL hardware-measured value in Joules (NOT calculated or estimated)
	EnergyConsumed   float64

	// Utilization
	SmUtilization    float64  // SM utilization (0.0-1.0)
	MemUtilization   float64  // Memory utilization (0.0-1.0)

	// Memory
	MemoryUsedBytes  uint64

	// Timing
	StartTime        time.Time
	EndTime          time.Time
	IsRunning        bool
}

// NewClient initializes a new DCGM client
func NewClient() (*Client, error) {
	slog.Info("Initializing DCGM client")

	// Initialize DCGM in Embedded mode
	cleanup, err := dcgm.Init(dcgm.Embedded)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize DCGM: %w", err)
	}

	// Store cleanup function for later
	// Note: cleanup should be called on shutdown
	_ = cleanup

	client := &Client{
		initialized: true,
	}

	return client, nil
}

// StartWatching begins per-process metrics collection
// This must be called once before GetProcessMetrics
func (c *Client) StartWatching() error {
	if !c.initialized {
		return fmt.Errorf("DCGM client not initialized")
	}

	slog.Info("Starting per-process metrics collection (WatchPidFields)")

	// Call WatchPidFields to enable per-process tracking
	// This configures DCGM to start recording stats for all GPU processes
	groupHandle, err := dcgm.WatchPidFields()
	if err != nil {
		return fmt.Errorf("failed to start watching PID fields: %w", err)
	}

	c.groupHandle = groupHandle

	slog.Info("Per-process metrics collection started",
		slog.Any("groupHandle", groupHandle))

	// Wait for initial data collection
	// DCGM needs time to collect first samples
	slog.Info("Waiting 3 seconds for initial DCGM data collection...")
	time.Sleep(3 * time.Second)

	return nil
}

// GetProcessMetrics retrieves metrics for a specific process
// Returns nil if process not found or no data available
func (c *Client) GetProcessMetrics(pid uint) (*ProcessMetrics, error) {
	if !c.initialized {
		return nil, fmt.Errorf("DCGM client not initialized")
	}

	// Get process info from DCGM
	processInfos, err := dcgm.GetProcessInfo(c.groupHandle, pid)
	if err != nil {
		return nil, fmt.Errorf("failed to get process info for PID %d: %w", pid, err)
	}

	if len(processInfos) == 0 {
		// Process not found or no GPU usage
		return nil, nil
	}

	// Use the first GPU's info (processes can use multiple GPUs)
	// TODO: In the future, we could return metrics for all GPUs the process uses
	info := processInfos[0]

	metrics := &ProcessMetrics{
		PID:         pid,
		GPU:         info.GPU,
		ProcessName: info.Name,
		IsRunning:   info.ProcessUtilization.EndTime == 0, // EndTime is 0 if still running
	}

	// Extract energy - THIS IS ACTUAL MEASURED ENERGY FROM GPU HARDWARE
	// NOT estimated or calculated
	if info.ProcessUtilization.EnergyConsumed != nil {
		metrics.EnergyConsumed = float64(*info.ProcessUtilization.EnergyConsumed)
	}

	// Extract utilization
	if info.ProcessUtilization.SmUtil != nil {
		metrics.SmUtilization = *info.ProcessUtilization.SmUtil / 100.0  // Convert to 0.0-1.0
	}
	if info.ProcessUtilization.MemUtil != nil {
		metrics.MemUtilization = *info.ProcessUtilization.MemUtil / 100.0  // Convert to 0.0-1.0
	}

	// Extract memory - GlobalUsed is int64, not a pointer
	slog.Debug("Memory info from DCGM",
		slog.Uint64("pid", uint64(pid)),
		slog.Int64("globalUsed", info.Memory.GlobalUsed))

	// Always set the value, even if it's 0, for debugging
	metrics.MemoryUsedBytes = uint64(info.Memory.GlobalUsed)

	// Extract timing - Convert dcgm.Time (uint64) to time.Time
	metrics.StartTime = time.Unix(int64(info.ProcessUtilization.StartTime), 0)
	metrics.EndTime = time.Unix(int64(info.ProcessUtilization.EndTime), 0)

	slog.Debug("Retrieved process metrics",
		slog.Uint64("pid", uint64(pid)),
		slog.Uint64("gpu", uint64(info.GPU)),
		slog.Float64("energy_joules", metrics.EnergyConsumed),
		slog.Bool("is_running", metrics.IsRunning))

	return metrics, nil
}

// Shutdown cleans up DCGM resources
func (c *Client) Shutdown() error {
	if !c.initialized {
		return nil
	}

	slog.Info("Shutting down DCGM client")
	dcgm.Shutdown()
	c.initialized = false

	return nil
}
