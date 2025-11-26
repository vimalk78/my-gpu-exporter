package process

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GetPodUID extracts the Kubernetes pod UID from a process's cgroup
// Returns empty string if process is not in a Kubernetes pod
func GetPodUID(pid uint) (string, error) {
	cgroupPath := fmt.Sprintf("/proc/%d/cgroup", pid)

	file, err := os.Open(cgroupPath)
	if err != nil {
		return "", fmt.Errorf("failed to open cgroup file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		podUID := extractPodUIDFromCgroupLine(line)
		if podUID != "" {
			return podUID, nil
		}
	}

	return "", nil
}

// extractPodUIDFromCgroupLine extracts pod UID from cgroup path
// Example: kubepods-besteffort-podd916368a_42f4_4dd8_a211_80caf2a7532a.slice
func extractPodUIDFromCgroupLine(line string) string {
	// Look for pod UID pattern: kubepods-...-pod<UID>.slice
	parts := strings.SplitN(line, ":", 3)
	if len(parts) < 3 {
		return ""
	}
	cgroupPath := parts[2]

	// Find "-pod" followed by UID
	idx := strings.Index(cgroupPath, "-pod")
	if idx == -1 {
		return ""
	}

	start := idx + len("-pod")
	// Find the end (.slice)
	end := strings.Index(cgroupPath[start:], ".slice")
	if end == -1 {
		return ""
	}

	uid := cgroupPath[start : start+end]
	// Convert underscores back to dashes (cgroup uses _ instead of -)
	uid = strings.ReplaceAll(uid, "_", "-")
	return uid
}

// GetContainerID extracts the container ID from a process's cgroup
// Returns empty string if process is not containerized
func GetContainerID(pid uint) (string, error) {
	// Read /proc/<pid>/cgroup
	cgroupPath := fmt.Sprintf("/proc/%d/cgroup", pid)

	file, err := os.Open(cgroupPath)
	if err != nil {
		return "", fmt.Errorf("failed to open cgroup file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Look for container ID in cgroup path
		// Example paths:
		// Docker: 12:memory:/docker/a1b2c3d4e5f6...
		// containerd: 12:memory:/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod<UUID>.slice/cri-containerd-<containerID>.scope
		// CRI-O: 12:memory:/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod<UUID>.slice/crio-<containerID>.scope

		containerID := extractContainerIDFromCgroupLine(line)
		if containerID != "" {
			return containerID, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading cgroup file: %w", err)
	}

	// Not a containerized process
	return "", nil
}

// extractContainerIDFromCgroupLine extracts container ID from a single cgroup line
func extractContainerIDFromCgroupLine(line string) string {
	// Split by colon: hierarchy-ID:controller-list:cgroup-path
	parts := strings.SplitN(line, ":", 3)
	if len(parts) < 3 {
		return ""
	}

	cgroupPath := parts[2]

	// containerd format: cri-containerd-<containerID>.scope
	if idx := strings.Index(cgroupPath, "cri-containerd-"); idx != -1 {
		start := idx + len("cri-containerd-")
		end := strings.Index(cgroupPath[start:], ".scope")
		if end != -1 {
			return cgroupPath[start : start+end]
		}
	}

	// CRI-O format: crio-<containerID>.scope
	if idx := strings.Index(cgroupPath, "crio-"); idx != -1 {
		start := idx + len("crio-")
		end := strings.Index(cgroupPath[start:], ".scope")
		if end != -1 {
			return cgroupPath[start : start+end]
		}
	}

	// Docker format: /docker/<containerID>
	if idx := strings.Index(cgroupPath, "/docker/"); idx != -1 {
		start := idx + len("/docker/")
		// Container ID is the next path component
		containerID := strings.Split(cgroupPath[start:], "/")[0]
		if len(containerID) >= 12 {  // Docker IDs are at least 12 chars
			return containerID
		}
	}

	// Podman format: /libpod-<containerID>.scope
	if idx := strings.Index(cgroupPath, "libpod-"); idx != -1 {
		start := idx + len("libpod-")
		end := strings.Index(cgroupPath[start:], ".scope")
		if end != -1 {
			return cgroupPath[start : start+end]
		}
	}

	return ""
}

// GetProcessName reads the process name from /proc/<pid>/comm
func GetProcessName(pid uint) (string, error) {
	commPath := fmt.Sprintf("/proc/%d/comm", pid)

	data, err := os.ReadFile(commPath)
	if err != nil {
		return "", fmt.Errorf("failed to read comm file: %w", err)
	}

	return strings.TrimSpace(string(data)), nil
}

// GetProcessCmdline reads the full command line from /proc/<pid>/cmdline
func GetProcessCmdline(pid uint) (string, error) {
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)

	data, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return "", fmt.Errorf("failed to read cmdline file: %w", err)
	}

	// cmdline uses null bytes as separators, replace with spaces
	cmdline := string(data)
	cmdline = strings.ReplaceAll(cmdline, "\x00", " ")
	return strings.TrimSpace(cmdline), nil
}

// IsProcessRunning checks if a process is still running
func IsProcessRunning(pid uint) bool {
	procPath := fmt.Sprintf("/proc/%d", pid)
	_, err := os.Stat(procPath)
	return err == nil
}

// GetProcRoot returns the proc filesystem root
// Useful for testing or when proc is mounted elsewhere
func GetProcRoot() string {
	if root := os.Getenv("PROC_ROOT"); root != "" {
		return root
	}
	return "/proc"
}

// GetProcessStartTime reads process start time from /proc/<pid>/stat
// Returns time in seconds since boot
func GetProcessStartTime(pid uint) (uint64, error) {
	statPath := filepath.Join(GetProcRoot(), fmt.Sprintf("%d/stat", pid))

	data, err := os.ReadFile(statPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read stat file: %w", err)
	}

	// Parse stat file - format is complex, we need field 22 (starttime)
	// Fields after comm (which can contain spaces/parens) start after last ')'
	statStr := string(data)
	lastParen := strings.LastIndex(statStr, ")")
	if lastParen == -1 {
		return 0, fmt.Errorf("invalid stat file format")
	}

	fields := strings.Fields(statStr[lastParen+1:])
	if len(fields) < 20 {
		return 0, fmt.Errorf("not enough fields in stat file")
	}

	// Field 22 is at index 19 (after comm)
	var starttime uint64
	_, err = fmt.Sscanf(fields[19], "%d", &starttime)
	if err != nil {
		return 0, fmt.Errorf("failed to parse start time: %w", err)
	}

	return starttime, nil
}
