# OpenShift Deployment Guide

## OpenShift Compatibility

Yes, my-gpu-exporter **can be deployed on OpenShift**, but requires some modifications due to OpenShift's enhanced security model.

## Key Differences from Kubernetes

OpenShift has stricter security defaults:

1. **Security Context Constraints (SCC)** - Controls what pods can do
2. **No privileged containers by default** - Must explicitly grant permission
3. **No hostPID/hostNetwork by default** - Requires privileged SCC
4. **Random user IDs** - Containers don't run as root by default
5. **Route instead of Ingress** - For external access

## Requirements

### Hardware/Software
- OpenShift 4.10+ (for GPU support)
- NVIDIA GPU Operator installed
- GPUs with Volta architecture or newer
- GPU accounting mode enabled on all GPU nodes

### Permissions
- Cluster admin access (to create SCC and grant permissions)
- GPU Operator already deployed

## Deployment Steps

### Step 1: Create Security Context Constraint

My-gpu-exporter requires privileged access. Create an SCC:

```yaml
# openshift-scc.yaml
apiVersion: security.openshift.io/v1
kind: SecurityContextConstraints
metadata:
  name: my-gpu-exporter-scc
allowHostDirVolumePlugin: true
allowHostIPC: false
allowHostNetwork: true
allowHostPID: true
allowHostPorts: false
allowPrivilegedContainer: true
allowedCapabilities:
  - SYS_ADMIN
defaultAddCapabilities: []
fsGroup:
  type: RunAsAny
groups: []
priority: null
readOnlyRootFilesystem: false
requiredDropCapabilities: []
runAsUser:
  type: RunAsAny
seLinuxContext:
  type: RunAsAny
supplementalGroups:
  type: RunAsAny
users: []
volumes:
  - configMap
  - downwardAPI
  - emptyDir
  - hostPath
  - persistentVolumeClaim
  - projected
  - secret
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-gpu-exporter
  namespace: gpu-monitoring
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: my-gpu-exporter-scc-role
rules:
  - apiGroups:
      - security.openshift.io
    resourceNames:
      - my-gpu-exporter-scc
    resources:
      - securitycontextconstraints
    verbs:
      - use
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: my-gpu-exporter-scc-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: my-gpu-exporter-scc-role
subjects:
  - kind: ServiceAccount
    name: my-gpu-exporter
    namespace: gpu-monitoring
```

Apply the SCC:
```bash
oc apply -f openshift-scc.yaml
```

### Step 2: Create OpenShift-Compatible DaemonSet

```yaml
# openshift-daemonset.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: gpu-monitoring
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: my-gpu-exporter
  namespace: gpu-monitoring
  labels:
    app: my-gpu-exporter
spec:
  selector:
    matchLabels:
      app: my-gpu-exporter
  template:
    metadata:
      labels:
        app: my-gpu-exporter
      annotations:
        # OpenShift will inject GPU device plugin resources
        openshift.io/scc: my-gpu-exporter-scc
    spec:
      # Required for accessing /proc and GPU devices
      hostPID: true
      hostNetwork: true

      # Use the service account with SCC permissions
      serviceAccountName: my-gpu-exporter

      # Init container to enable GPU accounting mode
      initContainers:
      - name: enable-accounting
        image: nvcr.io/nvidia/cuda:12.3.1-base-ubi8
        command: ["nvidia-smi", "-am", "1"]
        securityContext:
          privileged: true
          runAsUser: 0
        env:
        - name: NVIDIA_VISIBLE_DEVICES
          value: "all"
        - name: NVIDIA_DRIVER_CAPABILITIES
          value: "utility"

      containers:
      - name: exporter
        image: my-gpu-exporter:latest
        imagePullPolicy: IfNotPresent

        args:
        - --log-level=info
        - --kubernetes-enabled=true
        - --pod-resources-socket=/var/lib/kubelet/pod-resources/kubelet.sock
        - --metric-retention=5m
        - --process-scan-interval=10s

        ports:
        - name: metrics
          containerPort: 9400
          protocol: TCP

        # Required for GPU access and /proc access
        securityContext:
          privileged: true
          runAsUser: 0
          # OpenShift-specific: allow root
          allowPrivilegeEscalation: true

        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi

        livenessProbe:
          httpGet:
            path: /health
            port: 9400
          initialDelaySeconds: 30
          periodSeconds: 30
          timeoutSeconds: 5

        readinessProbe:
          httpGet:
            path: /health
            port: 9400
          initialDelaySeconds: 10
          periodSeconds: 10
          timeoutSeconds: 5

        env:
        - name: NVIDIA_VISIBLE_DEVICES
          value: "all"
        - name: NVIDIA_DRIVER_CAPABILITIES
          value: "compute,utility"

        volumeMounts:
        # Access to kubelet pod-resources API
        - name: pod-resources
          mountPath: /var/lib/kubelet/pod-resources
          readOnly: true

        # Access to /proc for cgroup parsing
        - name: proc
          mountPath: /proc
          readOnly: true

      # Node selector to only run on GPU nodes
      nodeSelector:
        nvidia.com/gpu.present: "true"

      # Tolerate GPU taints
      tolerations:
      - key: nvidia.com/gpu
        operator: Exists
        effect: NoSchedule
      - key: nvidia.com/gpu
        operator: Exists
        effect: PreferNoSchedule

      volumes:
      - name: pod-resources
        hostPath:
          path: /var/lib/kubelet/pod-resources
      - name: proc
        hostPath:
          path: /proc
---
apiVersion: v1
kind: Service
metadata:
  name: my-gpu-exporter
  namespace: gpu-monitoring
  labels:
    app: my-gpu-exporter
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "9400"
    prometheus.io/path: "/metrics"
spec:
  type: ClusterIP
  ports:
  - name: metrics
    port: 9400
    targetPort: 9400
    protocol: TCP
  selector:
    app: my-gpu-exporter
```

### Step 3: Deploy to OpenShift

```bash
# Create SCC and permissions
oc apply -f openshift-scc.yaml

# Deploy exporter
oc apply -f openshift-daemonset.yaml

# Verify deployment
oc -n gpu-monitoring get pods -l app=my-gpu-exporter

# Check logs
oc -n gpu-monitoring logs -l app=my-gpu-exporter
```

### Step 4: Create Route for External Access (Optional)

```yaml
# openshift-route.yaml
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: my-gpu-exporter
  namespace: gpu-monitoring
spec:
  to:
    kind: Service
    name: my-gpu-exporter
  port:
    targetPort: metrics
  tls:
    termination: edge
    insecureEdgeTerminationPolicy: Redirect
```

Apply:
```bash
oc apply -f openshift-route.yaml

# Get URL
oc -n gpu-monitoring get route my-gpu-exporter
```

## OpenShift-Specific Configurations

### 1. Use UBI-based Container Image

For OpenShift, use Red Hat UBI (Universal Base Image):

```dockerfile
# Dockerfile.ubi
# Build stage
FROM registry.access.redhat.com/ubi9/go-toolset:1.21 AS builder

WORKDIR /build

# Copy go mod files
COPY --chown=1001:0 go.mod go.sum ./
RUN go mod download

# Copy source code
COPY --chown=1001:0 . .

# Build binary
RUN CGO_ENABLED=1 GOOS=linux go build -o my-gpu-exporter .

# Runtime stage
FROM nvcr.io/nvidia/cuda:12.3.1-base-ubi8

# Install DCGM
RUN dnf install -y \
        https://developer.download.nvidia.com/compute/cuda/repos/rhel8/x86_64/cuda-rhel8.repo && \
    dnf install -y datacenter-gpu-manager && \
    dnf clean all

# Copy binary from builder
COPY --from=builder /build/my-gpu-exporter /usr/local/bin/my-gpu-exporter

# OpenShift runs with random UID, but we need root for GPU access
USER 0

EXPOSE 9400

ENTRYPOINT ["/usr/local/bin/my-gpu-exporter"]
```

Build:
```bash
docker build -f Dockerfile.ubi -t my-gpu-exporter:ubi .
```

### 2. Configure Prometheus Operator Integration

If using OpenShift's built-in Prometheus:

```yaml
# servicemonitor.yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: my-gpu-exporter
  namespace: gpu-monitoring
  labels:
    app: my-gpu-exporter
spec:
  selector:
    matchLabels:
      app: my-gpu-exporter
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
```

### 3. Node Selector for GPU Nodes

OpenShift with GPU Operator uses these labels:

```yaml
nodeSelector:
  nvidia.com/gpu.present: "true"
  # OR
  # feature.node.kubernetes.io/pci-10de.present: "true"  # NVIDIA vendor ID
```

Verify GPU node labels:
```bash
oc get nodes -o json | jq '.items[].metadata.labels' | grep nvidia
```

## Troubleshooting OpenShift Deployment

### SCC Permission Issues

**Symptom:**
```
Error: container has runAsNonRoot and image has non-numeric user (root)
```

**Solution:**
Verify SCC is applied:
```bash
# Check SCC
oc get scc my-gpu-exporter-scc

# Verify pod is using correct SCC
oc -n gpu-monitoring get pod <pod-name> -o yaml | grep scc
# Should show: openshift.io/scc: my-gpu-exporter-scc
```

Grant SCC manually if needed:
```bash
oc adm policy add-scc-to-user my-gpu-exporter-scc \
  -z my-gpu-exporter -n gpu-monitoring
```

### SELinux Issues

**Symptom:**
```
Error: cannot access /proc/<pid>/cgroup: Permission denied
```

**Solution:**
OpenShift uses SELinux. The privileged SCC should handle this, but verify:

```bash
# Check SELinux context
oc -n gpu-monitoring exec <pod-name> -- getenforce
# Should show: Permissive or Disabled (within container)

# Check host SELinux is not blocking
oc debug node/<gpu-node>
chroot /host
getenforce  # Check host SELinux
ausearch -m avc -ts recent  # Check for denials
```

### Pod Resources Socket Not Found

**Symptom:**
```
Error: stat /var/lib/kubelet/pod-resources/kubelet.sock: no such file or directory
```

**Solution:**
OpenShift may use different kubelet paths:

```bash
# Find actual socket path on node
oc debug node/<gpu-node>
chroot /host
find /var/lib/kubelet -name "*pod-resources*"

# Common paths:
# /var/lib/kubelet/pod-resources/kubelet.sock
# /var/lib/kubelet/plugins/kubernetes.io/pod-resources/kubelet.sock
```

Update DaemonSet volumeMount accordingly.

### GPU Not Accessible

**Symptom:**
```
Error: failed to initialize NVML
```

**Solution:**
Ensure GPU Operator is installed and working:

```bash
# Check GPU Operator
oc -n nvidia-gpu-operator get pods

# Verify driver daemonset
oc -n nvidia-gpu-operator get ds nvidia-driver-daemonset

# Check node has GPU
oc get node <gpu-node> -o json | jq '.status.capacity'
# Should show: nvidia.com/gpu: "X"
```

### Image Pull Errors

**Symptom:**
```
Error: ImagePullBackOff
```

**Solution:**
OpenShift may need ImageStream or specific registry:

```bash
# Create ImageStream (if using internal registry)
oc -n gpu-monitoring create imagestream my-gpu-exporter

# Tag image
oc -n gpu-monitoring import-image my-gpu-exporter:latest \
  --from=docker.io/your-registry/my-gpu-exporter:latest \
  --confirm

# Update DaemonSet to use ImageStreamTag
# image: image-registry.openshift-image-registry.svc:5000/gpu-monitoring/my-gpu-exporter:latest
```

## Integration with OpenShift Monitoring

### Option 1: User Workload Monitoring

Enable user workload monitoring in OpenShift:

```bash
# Check if enabled
oc -n openshift-user-workload-monitoring get pods

# Enable if not already
oc -n openshift-monitoring edit configmap cluster-monitoring-config
# Add:
# data:
#   config.yaml: |
#     enableUserWorkload: true
```

Create ServiceMonitor:
```bash
oc apply -f servicemonitor.yaml
```

### Option 2: Add to Cluster Monitoring

For cluster-wide visibility, integrate with OpenShift cluster monitoring:

```yaml
# prometheus-rule.yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: my-gpu-exporter-alerts
  namespace: gpu-monitoring
spec:
  groups:
  - name: gpu-power-alerts
    interval: 30s
    rules:
    - alert: HighGPUProcessPower
      expr: rate(my_gpu_process_energy_joules[5m]) > 300
      for: 10m
      labels:
        severity: warning
      annotations:
        summary: "GPU process {{ $labels.pod }} in {{ $labels.namespace }} consuming > 300W"
```

## OpenShift Console Integration

Add metrics to OpenShift console:

1. **Add to Developer Console:**
   - Navigate to Monitoring → Metrics
   - Query: `rate(my_gpu_process_energy_joules[5m])`

2. **Create Dashboard:**
   ```yaml
   apiVersion: v1
   kind: ConfigMap
   metadata:
     name: gpu-power-dashboard
     namespace: gpu-monitoring
   data:
     dashboard.json: |
       {
         "dashboard": {
           "title": "GPU Power Consumption",
           "panels": [...]
         }
       }
   ```

## Security Best Practices for OpenShift

1. **Limit SCC Usage:**
   ```bash
   # Only grant to specific service account
   oc adm policy add-scc-to-user my-gpu-exporter-scc \
     -z my-gpu-exporter -n gpu-monitoring
   ```

2. **Network Policies:**
   ```yaml
   apiVersion: networking.k8s.io/v1
   kind: NetworkPolicy
   metadata:
     name: my-gpu-exporter-netpol
     namespace: gpu-monitoring
   spec:
     podSelector:
       matchLabels:
         app: my-gpu-exporter
     policyTypes:
     - Ingress
     ingress:
     - from:
       - namespaceSelector:
           matchLabels:
             name: openshift-monitoring
       ports:
       - protocol: TCP
         port: 9400
   ```

3. **RBAC Restrictions:**
   - Only grant minimum required permissions
   - Use namespace-scoped roles where possible

## Production Checklist for OpenShift

- [ ] GPU Operator installed and operational
- [ ] Security Context Constraint created and applied
- [ ] Service account has SCC permissions
- [ ] UBI-based image built and pushed to registry
- [ ] GPU accounting mode enabled on all GPU nodes
- [ ] DaemonSet deployed successfully
- [ ] Pods running on all GPU nodes
- [ ] Metrics visible in OpenShift Console
- [ ] ServiceMonitor created (if using Prometheus Operator)
- [ ] Alerts configured
- [ ] Network policies applied
- [ ] Documentation for team

## Summary

**Yes, my-gpu-exporter works on OpenShift with these modifications:**

1. ✅ Create custom SecurityContextConstraint
2. ✅ Grant SCC to service account
3. ✅ Use UBI-based container image (optional but recommended)
4. ✅ Configure OpenShift-specific node selectors
5. ✅ Integrate with OpenShift monitoring stack
6. ✅ Apply network policies and RBAC

The main difference is OpenShift's enhanced security requiring explicit SCC permissions for privileged containers.
