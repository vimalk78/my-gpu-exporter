.PHONY: build clean docker-build docker-push test fmt vet deps deploy-openshift deploy-k8s deploy-all undeploy-openshift undeploy-k8s status logs

# Variables
BINARY_NAME=my-gpu-exporter
REGISTRY?=quay.io/vimalkum
IMAGE_NAME?=my-gpu-exporter
DOCKER_IMAGE?=$(REGISTRY)/$(IMAGE_NAME)
DOCKER_TAG?=latest
NAMESPACE?=gpu-monitoring

# Build the binary
build:
	CGO_ENABLED=1 go build -mod=vendor -o $(BINARY_NAME) .

# Clean build artifacts
clean:
	rm -f $(BINARY_NAME)
	go clean

# Build Docker image
docker-build:
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

# Build UBI-based Docker image for OpenShift
docker-build-ubi:
	docker build -f Dockerfile.ubi -t $(DOCKER_IMAGE):$(DOCKER_TAG)-ubi .

# Push Docker image
docker-push: docker-build
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

# Push UBI image
docker-push-ubi: docker-build-ubi
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)-ubi

# Run tests
test:
	go test -v ./...

# Format code
fmt:
	go fmt ./...

# Run go vet
vet:
	go vet ./...

# Download dependencies
deps:
	go mod download
	go mod tidy

# Run locally (requires GPU and DCGM)
run: build
	sudo ./$(BINARY_NAME) --log-level=debug

# Install binary
install: build
	sudo cp $(BINARY_NAME) /usr/local/bin/

# Uninstall binary
uninstall:
	sudo rm -f /usr/local/bin/$(BINARY_NAME)

# ===== Deployment Targets =====

# Deploy to OpenShift
deploy-openshift:
	@echo "Deploying my-gpu-exporter to OpenShift..."
	@echo "Creating namespace and DaemonSet..."
	oc apply -f kubernetes/openshift-daemonset.yaml
	@echo "Applying SCC..."
	oc apply -f kubernetes/openshift-scc.yaml
	@echo "Creating ServiceMonitor..."
	oc apply -f kubernetes/servicemonitor.yaml
	@echo "Granting SCC permissions..."
	oc adm policy add-scc-to-user my-gpu-exporter-scc -z my-gpu-exporter -n $(NAMESPACE) || true
	@echo "Deployment complete! Check status with: make status"

# Deploy to Kubernetes
deploy-k8s:
	@echo "Deploying my-gpu-exporter to Kubernetes..."
	kubectl apply -f kubernetes/daemonset.yaml
	kubectl apply -f kubernetes/servicemonitor.yaml
	@echo "Deployment complete! Check status with: make status"

# Build, push, and deploy to OpenShift (complete workflow)
deploy-all:
	@echo "Building, pushing, and deploying my-gpu-exporter..."
	$(MAKE) build
	podman build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .
	podman push $(DOCKER_IMAGE):$(DOCKER_TAG)
	$(MAKE) deploy-openshift

# Undeploy from OpenShift
undeploy-openshift:
	@echo "Removing my-gpu-exporter from OpenShift..."
	oc delete -f kubernetes/servicemonitor.yaml || true
	oc delete -f kubernetes/openshift-daemonset.yaml || true
	oc delete -f kubernetes/openshift-scc.yaml || true
	@echo "Cleanup complete!"

# Undeploy from Kubernetes
undeploy-k8s:
	@echo "Removing my-gpu-exporter from Kubernetes..."
	kubectl delete -f kubernetes/servicemonitor.yaml || true
	kubectl delete -f kubernetes/daemonset.yaml || true
	@echo "Cleanup complete!"

# Check deployment status
status:
	@echo "=== DaemonSet Status ==="
	oc get daemonset -n $(NAMESPACE) my-gpu-exporter 2>/dev/null || kubectl get daemonset -n $(NAMESPACE) my-gpu-exporter 2>/dev/null || echo "DaemonSet not found"
	@echo ""
	@echo "=== Pod Status ==="
	oc get pods -n $(NAMESPACE) -l app=my-gpu-exporter 2>/dev/null || kubectl get pods -n $(NAMESPACE) -l app=my-gpu-exporter 2>/dev/null || echo "No pods found"
	@echo ""
	@echo "=== ServiceMonitor Status ==="
	oc get servicemonitor -n $(NAMESPACE) my-gpu-exporter 2>/dev/null || kubectl get servicemonitor -n $(NAMESPACE) my-gpu-exporter 2>/dev/null || echo "ServiceMonitor not found"

# View logs from all exporter pods
logs:
	@echo "Tailing logs from my-gpu-exporter pods..."
	oc logs -n $(NAMESPACE) -l app=my-gpu-exporter --tail=50 -f 2>/dev/null || kubectl logs -n $(NAMESPACE) -l app=my-gpu-exporter --tail=50 -f 2>/dev/null || echo "No pods found"
