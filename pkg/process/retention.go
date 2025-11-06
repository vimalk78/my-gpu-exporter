package process

import (
	"log/slog"
	"sync"
	"time"
)

// RetentionManager handles keeping metrics for processes that have exited
type RetentionManager struct {
	mu              sync.RWMutex
	exitedProcesses map[uint]time.Time  // PID -> exit time
	retention       time.Duration
}

// NewRetentionManager creates a new retention manager
func NewRetentionManager(retention time.Duration) *RetentionManager {
	return &RetentionManager{
		exitedProcesses: make(map[uint]time.Time),
		retention:       retention,
	}
}

// MarkExited marks a process as exited
func (rm *RetentionManager) MarkExited(pid uint) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if _, exists := rm.exitedProcesses[pid]; !exists {
		rm.exitedProcesses[pid] = time.Now()
		slog.Debug("Marked process as exited",
			slog.Uint64("pid", uint64(pid)))
	}
}

// IsExited checks if a process is marked as exited
func (rm *RetentionManager) IsExited(pid uint) bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	_, exists := rm.exitedProcesses[pid]
	return exists
}

// ShouldRetain checks if an exited process should still be retained
func (rm *RetentionManager) ShouldRetain(pid uint) bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	exitTime, exists := rm.exitedProcesses[pid]
	if !exists {
		return false
	}

	return time.Since(exitTime) < rm.retention
}

// CleanupExpired removes processes that have exceeded retention period
func (rm *RetentionManager) CleanupExpired() int {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	now := time.Now()
	removed := 0

	for pid, exitTime := range rm.exitedProcesses {
		if now.Sub(exitTime) >= rm.retention {
			delete(rm.exitedProcesses, pid)
			removed++
			slog.Debug("Removed expired process from retention",
				slog.Uint64("pid", uint64(pid)),
				slog.Duration("age", now.Sub(exitTime)))
		}
	}

	if removed > 0 {
		slog.Info("Cleaned up expired processes",
			slog.Int("removed", removed),
			slog.Int("remaining", len(rm.exitedProcesses)))
	}

	return removed
}

// GetExitedProcesses returns all PIDs currently in retention
func (rm *RetentionManager) GetExitedProcesses() []uint {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	pids := make([]uint, 0, len(rm.exitedProcesses))
	for pid := range rm.exitedProcesses {
		pids = append(pids, pid)
	}

	return pids
}

// GetExitTime returns the exit time for a process
func (rm *RetentionManager) GetExitTime(pid uint) (time.Time, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	exitTime, exists := rm.exitedProcesses[pid]
	return exitTime, exists
}

// Count returns the number of processes in retention
func (rm *RetentionManager) Count() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	return len(rm.exitedProcesses)
}
