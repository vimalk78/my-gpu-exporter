#!/bin/bash
set -e

# Start nv-hostengine in the background
echo "Starting nv-hostengine..."
nv-hostengine &

# Wait a moment for nv-hostengine to start
sleep 2

# Start the exporter (this becomes PID 1 replacement)
echo "Starting my-gpu-exporter..."
exec /usr/local/bin/my-gpu-exporter "$@"
