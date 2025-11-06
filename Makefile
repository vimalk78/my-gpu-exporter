.PHONY: build clean docker-build docker-push test fmt vet deps

# Variables
BINARY_NAME=my-gpu-exporter
DOCKER_IMAGE?=my-gpu-exporter
DOCKER_TAG?=latest

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
