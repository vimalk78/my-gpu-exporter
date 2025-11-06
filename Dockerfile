# Build stage
FROM golang:1.24-bullseye AS builder

WORKDIR /build

# Copy source code and vendor directory
COPY . .

# Build binary using vendored dependencies
RUN CGO_ENABLED=1 GOOS=linux go build -mod=vendor -o my-gpu-exporter .

# Runtime stage - use the same DCGM image that GPU Operator uses
FROM nvcr.io/nvidia/cloud-native/dcgm:3.3.9-1-ubi9

# Install procps for ps command
RUN dnf install -y procps-ng && dnf clean all

# Create symlink for libdcgm.so.4 (go-dcgm expects v4 but image has v3)
RUN ln -s /usr/lib64/libdcgm.so.3 /usr/lib64/libdcgm.so.4 && ldconfig

# Copy binary from builder
COPY --from=builder /build/my-gpu-exporter /usr/local/bin/my-gpu-exporter

# Copy entrypoint script
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# Create non-root user
RUN useradd -r -u 1000 -g root exporter

# Note: Despite creating a non-root user, the container will need to run
# as root or with CAP_SYS_ADMIN to access GPU metrics and /proc

EXPOSE 9400

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
