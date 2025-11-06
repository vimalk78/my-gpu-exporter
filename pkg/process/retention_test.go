package process

import (
	"testing"
	"time"
)

func TestRetentionManager_MarkExited(t *testing.T) {
	rm := NewRetentionManager(5 * time.Minute)

	// Mark a process as exited
	pid := uint(12345)
	rm.MarkExited(pid)

	// Check it's marked as exited
	if !rm.IsExited(pid) {
		t.Errorf("Process %d should be marked as exited", pid)
	}

	// Check it should be retained (just marked)
	if !rm.ShouldRetain(pid) {
		t.Errorf("Process %d should be retained (just exited)", pid)
	}

	// Double marking should be idempotent
	rm.MarkExited(pid)
	if rm.Count() != 1 {
		t.Errorf("Expected 1 process in retention, got %d", rm.Count())
	}
}

func TestRetentionManager_ShouldRetain(t *testing.T) {
	rm := NewRetentionManager(100 * time.Millisecond)

	pid := uint(12345)
	rm.MarkExited(pid)

	// Should be retained immediately
	if !rm.ShouldRetain(pid) {
		t.Error("Process should be retained immediately after exit")
	}

	// Wait for retention period to expire
	time.Sleep(150 * time.Millisecond)

	// Should no longer be retained
	if rm.ShouldRetain(pid) {
		t.Error("Process should not be retained after expiration")
	}
}

func TestRetentionManager_CleanupExpired(t *testing.T) {
	rm := NewRetentionManager(100 * time.Millisecond)

	// Add multiple processes
	pids := []uint{1, 2, 3, 4, 5}
	for _, pid := range pids {
		rm.MarkExited(pid)
	}

	if rm.Count() != 5 {
		t.Errorf("Expected 5 processes, got %d", rm.Count())
	}

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Cleanup should remove all
	removed := rm.CleanupExpired()
	if removed != 5 {
		t.Errorf("Expected to remove 5 processes, removed %d", removed)
	}

	if rm.Count() != 0 {
		t.Errorf("Expected 0 processes after cleanup, got %d", rm.Count())
	}
}

func TestRetentionManager_CleanupPartial(t *testing.T) {
	rm := NewRetentionManager(200 * time.Millisecond)

	// Add first batch
	rm.MarkExited(1)
	rm.MarkExited(2)

	// Wait 150ms
	time.Sleep(150 * time.Millisecond)

	// Add second batch (should not expire yet)
	rm.MarkExited(3)
	rm.MarkExited(4)

	// Wait another 100ms (first batch expired, second batch not yet)
	time.Sleep(100 * time.Millisecond)

	// Cleanup should remove only first batch
	removed := rm.CleanupExpired()
	if removed != 2 {
		t.Errorf("Expected to remove 2 processes, removed %d", removed)
	}

	if rm.Count() != 2 {
		t.Errorf("Expected 2 processes remaining, got %d", rm.Count())
	}

	// Verify correct PIDs remain
	if !rm.IsExited(3) || !rm.IsExited(4) {
		t.Error("PIDs 3 and 4 should still be marked as exited")
	}

	if rm.IsExited(1) || rm.IsExited(2) {
		t.Error("PIDs 1 and 2 should have been cleaned up")
	}
}

func TestRetentionManager_GetExitTime(t *testing.T) {
	rm := NewRetentionManager(5 * time.Minute)

	pid := uint(12345)
	beforeMark := time.Now()
	rm.MarkExited(pid)
	afterMark := time.Now()

	exitTime, exists := rm.GetExitTime(pid)
	if !exists {
		t.Error("Exit time should exist for marked process")
	}

	if exitTime.Before(beforeMark) || exitTime.After(afterMark) {
		t.Error("Exit time should be between beforeMark and afterMark")
	}

	// Non-existent PID
	_, exists = rm.GetExitTime(99999)
	if exists {
		t.Error("Exit time should not exist for non-existent PID")
	}
}

func TestRetentionManager_GetExitedProcesses(t *testing.T) {
	rm := NewRetentionManager(5 * time.Minute)

	// Add processes
	expectedPIDs := map[uint]bool{1: true, 2: true, 3: true}
	for pid := range expectedPIDs {
		rm.MarkExited(pid)
	}

	// Get exited processes
	pids := rm.GetExitedProcesses()

	if len(pids) != len(expectedPIDs) {
		t.Errorf("Expected %d PIDs, got %d", len(expectedPIDs), len(pids))
	}

	// Verify all PIDs are present
	for _, pid := range pids {
		if !expectedPIDs[pid] {
			t.Errorf("Unexpected PID %d in exited processes", pid)
		}
	}
}

func TestRetentionManager_ZeroRetention(t *testing.T) {
	rm := NewRetentionManager(0)

	pid := uint(12345)
	rm.MarkExited(pid)

	// With zero retention, should not be retained
	if rm.ShouldRetain(pid) {
		t.Error("Process should not be retained with zero retention period")
	}

	// Cleanup should remove immediately
	removed := rm.CleanupExpired()
	if removed != 1 {
		t.Errorf("Expected to remove 1 process, removed %d", removed)
	}
}

func TestRetentionManager_ConcurrentAccess(t *testing.T) {
	rm := NewRetentionManager(1 * time.Second)

	// Concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(pid uint) {
			rm.MarkExited(pid)
			rm.IsExited(pid)
			rm.ShouldRetain(pid)
			done <- true
		}(uint(i))
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	if rm.Count() != 10 {
		t.Errorf("Expected 10 processes, got %d", rm.Count())
	}

	// Concurrent cleanup
	go rm.CleanupExpired()
	go rm.CleanupExpired()
	time.Sleep(100 * time.Millisecond) // Let them complete
}
