package collector

import (
	"testing"
	"time"

	"github.com/vimalk78/my-gpu-exporter/pkg/config"
	"github.com/vimalk78/my-gpu-exporter/pkg/process"
)

// TestCollector_RetentionCleanup tests that metrics are properly cleaned up after retention expires
func TestCollector_RetentionCleanup(t *testing.T) {
	// Create collector with 200ms retention
	retention := process.NewRetentionManager(200 * time.Millisecond)

	collector := &Collector{
		config:          &config.Config{},
		retention:       retention,
		processMetrics:  make(map[uint]*ProcessMetrics),
	}

	// Add some metrics for "exited" processes
	pid1 := uint(1000)
	pid2 := uint(2000)
	pid3 := uint(3000)

	collector.processMetrics[pid1] = &ProcessMetrics{
		PID:          pid1,
		ProcessName:  "test1",
		EnergyJoules: 100,
		IsRunning:    false,
	}
	collector.processMetrics[pid2] = &ProcessMetrics{
		PID:          pid2,
		ProcessName:  "test2",
		EnergyJoules: 200,
		IsRunning:    false,
	}
	collector.processMetrics[pid3] = &ProcessMetrics{
		PID:          pid3,
		ProcessName:  "test3",
		EnergyJoules: 300,
		IsRunning:    true, // This one is still running
	}

	// Mark first two as exited
	retention.MarkExited(pid1)
	retention.MarkExited(pid2)

	// Verify all three are in metrics
	if len(collector.processMetrics) != 3 {
		t.Errorf("Expected 3 processes in metrics, got %d", len(collector.processMetrics))
	}

	// Wait for retention to expire
	time.Sleep(250 * time.Millisecond)

	// Simulate the cleanup logic from Collect()
	collector.mu.Lock()
	for _, pid := range retention.GetExitedProcesses() {
		if !retention.ShouldRetain(pid) {
			delete(collector.processMetrics, pid)
		}
	}
	collector.mu.Unlock()

	retention.CleanupExpired()

	// Should only have pid3 left (still running)
	if len(collector.processMetrics) != 1 {
		t.Errorf("Expected 1 process remaining, got %d", len(collector.processMetrics))
	}

	if _, exists := collector.processMetrics[pid3]; !exists {
		t.Error("PID 3 (running process) should still be in metrics")
	}

	if _, exists := collector.processMetrics[pid1]; exists {
		t.Error("PID 1 should have been removed from metrics")
	}

	if _, exists := collector.processMetrics[pid2]; exists {
		t.Error("PID 2 should have been removed from metrics")
	}
}

// TestCollector_PartialRetentionCleanup tests that only expired processes are removed
func TestCollector_PartialRetentionCleanup(t *testing.T) {
	retention := process.NewRetentionManager(300 * time.Millisecond)

	collector := &Collector{
		config:          &config.Config{},
		retention:       retention,
		processMetrics:  make(map[uint]*ProcessMetrics),
	}

	// Add first process
	pid1 := uint(1000)
	collector.processMetrics[pid1] = &ProcessMetrics{
		PID:          pid1,
		ProcessName:  "old",
		EnergyJoules: 100,
		IsRunning:    false,
	}
	retention.MarkExited(pid1)

	// Wait 150ms
	time.Sleep(150 * time.Millisecond)

	// Add second process (should not expire yet)
	pid2 := uint(2000)
	collector.processMetrics[pid2] = &ProcessMetrics{
		PID:          pid2,
		ProcessName:  "new",
		EnergyJoules: 200,
		IsRunning:    false,
	}
	retention.MarkExited(pid2)

	// Wait another 200ms (pid1 should expire, pid2 should not)
	time.Sleep(200 * time.Millisecond)

	// Run cleanup
	collector.mu.Lock()
	for _, pid := range retention.GetExitedProcesses() {
		if !retention.ShouldRetain(pid) {
			delete(collector.processMetrics, pid)
		}
	}
	collector.mu.Unlock()

	retention.CleanupExpired()

	// Should have 1 process left (pid2)
	if len(collector.processMetrics) != 1 {
		t.Errorf("Expected 1 process remaining, got %d", len(collector.processMetrics))
	}

	if _, exists := collector.processMetrics[pid2]; !exists {
		t.Error("PID 2 should still be in metrics (not expired)")
	}

	if _, exists := collector.processMetrics[pid1]; exists {
		t.Error("PID 1 should have been removed (expired)")
	}
}

// TestCollector_ZeroRetention tests immediate cleanup with zero retention
func TestCollector_ZeroRetention(t *testing.T) {
	retention := process.NewRetentionManager(0)

	collector := &Collector{
		config:          &config.Config{},
		retention:       retention,
		processMetrics:  make(map[uint]*ProcessMetrics),
	}

	// Add exited process
	pid := uint(1000)
	collector.processMetrics[pid] = &ProcessMetrics{
		PID:          pid,
		ProcessName:  "test",
		EnergyJoules: 100,
		IsRunning:    false,
	}
	retention.MarkExited(pid)

	// Run cleanup immediately (no wait)
	collector.mu.Lock()
	for _, pid := range retention.GetExitedProcesses() {
		if !retention.ShouldRetain(pid) {
			delete(collector.processMetrics, pid)
		}
	}
	collector.mu.Unlock()

	retention.CleanupExpired()

	// Should be removed immediately
	if len(collector.processMetrics) != 0 {
		t.Errorf("Expected 0 processes with zero retention, got %d", len(collector.processMetrics))
	}
}

// TestCollector_GetMetrics tests that GetMetrics returns correct snapshot
func TestCollector_GetMetrics(t *testing.T) {
	retention := process.NewRetentionManager(5 * time.Minute)

	collector := &Collector{
		config:          &config.Config{},
		retention:       retention,
		processMetrics:  make(map[uint]*ProcessMetrics),
	}

	// Add metrics
	pid1 := uint(1000)
	pid2 := uint(2000)

	collector.processMetrics[pid1] = &ProcessMetrics{
		PID:          pid1,
		ProcessName:  "running",
		EnergyJoules: 100,
		IsRunning:    true,
	}

	collector.processMetrics[pid2] = &ProcessMetrics{
		PID:          pid2,
		ProcessName:  "exited",
		EnergyJoules: 200,
		IsRunning:    false,
	}
	retention.MarkExited(pid2)

	// Get metrics
	metrics := collector.GetMetrics()

	// Should have both
	if len(metrics) != 2 {
		t.Errorf("Expected 2 processes in metrics, got %d", len(metrics))
	}

	// Verify values
	if metrics[pid1].EnergyJoules != 100 {
		t.Errorf("Expected energy 100 for pid1, got %f", metrics[pid1].EnergyJoules)
	}

	if metrics[pid2].EnergyJoules != 200 {
		t.Errorf("Expected energy 200 for pid2, got %f", metrics[pid2].EnergyJoules)
	}

	// Modify returned metrics should not affect collector
	metrics[pid1].EnergyJoules = 999
	if collector.processMetrics[pid1].EnergyJoules == 999 {
		t.Error("Modifying returned metrics should not affect collector's internal state")
	}
}
