package kubernetes

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"
)

const (
	connectionTimeout   = 10 * time.Second
	defaultSocketPath   = "/var/lib/kubelet/pod-resources/kubelet.sock"
	nvidiaResourceName  = "nvidia.com/gpu"
)

// PodInfo contains Kubernetes pod metadata
type PodInfo struct {
	PodName       string
	PodNamespace  string
	ContainerName string
	ContainerID   string
}

// PodMapper maps container IDs to Kubernetes pod information
type PodMapper struct {
	socketPath   string
	cache        map[string]*PodInfo // keyed by container_id or pod_uid
	uidCache     map[string]*PodInfo // keyed by pod_uid
	lastUpdate   time.Time
	k8sAPIClient *http.Client
	k8sToken     string
}

// NewPodMapper creates a new pod mapper
func NewPodMapper(socketPath string) *PodMapper {
	if socketPath == "" {
		socketPath = defaultSocketPath
	}

	pm := &PodMapper{
		socketPath: socketPath,
		cache:      make(map[string]*PodInfo),
		uidCache:   make(map[string]*PodInfo),
	}

	// Set up K8s API client for in-cluster access
	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err == nil {
		pm.k8sToken = string(token)
		pm.k8sAPIClient = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
		slog.Debug("Kubernetes API client initialized")
	} else {
		slog.Debug("No service account token found, pod UID lookup disabled")
	}

	return pm
}

// GetPodInfo returns pod information for a given container ID
// Returns nil if container is not part of a Kubernetes pod
func (pm *PodMapper) GetPodInfo(containerID string) (*PodInfo, error) {
	// Refresh cache if stale (older than 30 seconds)
	if time.Since(pm.lastUpdate) > 30*time.Second {
		if err := pm.refreshCache(); err != nil {
			return nil, fmt.Errorf("failed to refresh pod cache: %w", err)
		}
	}

	// Lookup in cache by container ID
	if info, ok := pm.cache[containerID]; ok {
		return info, nil
	}

	// Not found - not a Kubernetes pod
	return nil, nil
}

// GetPodInfoByUID returns pod information for a given pod UID
func (pm *PodMapper) GetPodInfoByUID(podUID string) (*PodInfo, error) {
	// Check UID cache first
	if info, ok := pm.uidCache[podUID]; ok {
		return info, nil
	}

	// Query Kubernetes API for pod info
	if pm.k8sAPIClient != nil && podUID != "" {
		info, err := pm.queryPodByUID(podUID)
		if err != nil {
			slog.Debug("Failed to query pod by UID", slog.String("uid", podUID), slog.String("error", err.Error()))
		} else if info != nil {
			pm.uidCache[podUID] = info
			return info, nil
		}
	}

	return nil, nil
}

// queryPodByUID queries Kubernetes API to get pod info by UID
func (pm *PodMapper) queryPodByUID(podUID string) (*PodInfo, error) {
	if pm.k8sAPIClient == nil {
		return nil, nil
	}

	// Check cache first
	if info, ok := pm.uidCache[podUID]; ok {
		return info, nil
	}

	// Not in cache - refresh and try again (new pod may have been created)
	if err := pm.refreshUIDCache(); err != nil {
		return nil, err
	}

	if info, ok := pm.uidCache[podUID]; ok {
		return info, nil
	}

	return nil, nil
}

// refreshUIDCache fetches all pods from K8s API and builds UID -> PodInfo cache
func (pm *PodMapper) refreshUIDCache() error {
	if pm.k8sAPIClient == nil {
		return nil
	}

	// Get API server address from environment (works with hostNetwork)
	apiHost := os.Getenv("KUBERNETES_SERVICE_HOST")
	apiPort := os.Getenv("KUBERNETES_SERVICE_PORT")
	if apiHost == "" {
		apiHost = "kubernetes.default.svc"
	}
	if apiPort == "" {
		apiPort = "443"
	}

	url := fmt.Sprintf("https://%s:%s/api/v1/pods", apiHost, apiPort)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+pm.k8sToken)

	resp, err := pm.k8sAPIClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("K8s API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				UID       string `json:"uid"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Name string `json:"name"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	// Build UID cache
	for _, pod := range result.Items {
		containerName := ""
		if len(pod.Spec.Containers) > 0 {
			containerName = pod.Spec.Containers[0].Name // Use first container
		}
		pm.uidCache[pod.Metadata.UID] = &PodInfo{
			PodName:       pod.Metadata.Name,
			PodNamespace:  pod.Metadata.Namespace,
			ContainerName: containerName,
		}
	}

	slog.Debug("Refreshed pod UID cache", slog.Int("pods", len(result.Items)))
	return nil
}

// refreshCache updates the pod information cache
func (pm *PodMapper) refreshCache() error {
	slog.Debug("Refreshing Kubernetes pod cache")

	// Connect to kubelet pod-resources API
	conn, cleanup, err := connectToKubelet(pm.socketPath)
	if err != nil {
		return err
	}
	defer cleanup()

	// List pod resources
	pods, err := listPodResources(conn)
	if err != nil {
		return err
	}

	// Build new cache
	newCache := make(map[string]*PodInfo)

	for _, pod := range pods.GetPodResources() {
		podName := pod.GetName()
		podNamespace := pod.GetNamespace()

		for _, container := range pod.GetContainers() {
			containerName := container.GetName()

			// Get container ID from devices
			// The container ID is embedded in device IDs for GPU resources
			for _, device := range container.GetDevices() {
				resourceName := device.GetResourceName()

				// Check if this is an NVIDIA GPU resource
				if !strings.HasPrefix(resourceName, nvidiaResourceName) &&
					!strings.HasPrefix(resourceName, "nvidia.com/mig") {
					continue
				}

				// For Kubernetes, the container ID is available via the runtime
				// We'll need to extract it from the pod resources response
				// Note: The pod-resources API doesn't directly provide container IDs
				// We need to get it from the container runtime or cgroup info

				// For now, we'll use a workaround: extract from device IDs if available
				// or we'll populate this mapping in the collector by cross-referencing
				// PIDs with container IDs from cgroup

				// Store pod info with a placeholder container ID
				// The collector will update this with actual container IDs
				info := &PodInfo{
					PodName:       podName,
					PodNamespace:  podNamespace,
					ContainerName: containerName,
				}

				// Try to extract container ID from device IDs if available
				for _, deviceID := range device.GetDeviceIds() {
					// Device IDs sometimes contain container ID
					if cid := extractContainerIDFromDeviceID(deviceID); cid != "" {
						info.ContainerID = cid
						newCache[cid] = info
					}
				}

				// Also store by pod+container key for lookup
				key := fmt.Sprintf("%s/%s/%s", podNamespace, podName, containerName)
				newCache[key] = info

				slog.Debug("Cached pod info",
					slog.String("pod", podName),
					slog.String("namespace", podNamespace),
					slog.String("container", containerName))
			}
		}
	}

	pm.cache = newCache
	pm.lastUpdate = time.Now()

	slog.Debug("Pod cache refreshed", slog.Int("entries", len(newCache)))

	return nil
}

// AddContainerMapping manually adds a container ID to pod info mapping
// Used by collector to populate cache with actual container IDs from cgroup
func (pm *PodMapper) AddContainerMapping(containerID string, info *PodInfo) {
	pm.cache[containerID] = info
}

// GetPodInfoByPodContainer looks up pod info by pod name and container name
func (pm *PodMapper) GetPodInfoByPodContainer(namespace, podName, containerName string) (*PodInfo, error) {
	key := fmt.Sprintf("%s/%s/%s", namespace, podName, containerName)

	if info, ok := pm.cache[key]; ok {
		return info, nil
	}

	// Try refreshing cache
	if err := pm.refreshCache(); err != nil {
		return nil, err
	}

	if info, ok := pm.cache[key]; ok {
		return info, nil
	}

	return nil, nil
}

// connectToKubelet establishes gRPC connection to kubelet
func connectToKubelet(socketPath string) (*grpc.ClientConn, func(), error) {
	// Use unix:// scheme for gRPC to properly resolve the socket path
	target := "unix://" + socketPath
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to kubelet socket: %w", err)
	}

	cleanup := func() {
		conn.Close()
	}

	return conn, cleanup, nil
}

// listPodResources queries kubelet for pod resources
func listPodResources(conn *grpc.ClientConn) (*podresourcesapi.ListPodResourcesResponse, error) {
	client := podresourcesapi.NewPodResourcesListerClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
	defer cancel()

	resp, err := client.List(ctx, &podresourcesapi.ListPodResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pod resources: %w", err)
	}

	return resp, nil
}

// extractContainerIDFromDeviceID attempts to extract container ID from device ID
func extractContainerIDFromDeviceID(deviceID string) string {
	// Some device plugins include container ID in the device ID
	// Format varies by plugin, this is a best-effort extraction

	// containerd format: might have cri-containerd-<containerID>
	if idx := strings.Index(deviceID, "cri-containerd-"); idx != -1 {
		start := idx + len("cri-containerd-")
		// Extract until next delimiter
		for i, ch := range deviceID[start:] {
			if ch == '-' || ch == '/' || ch == '.' {
				return deviceID[start : start+i]
			}
		}
		return deviceID[start:]
	}

	return ""
}
