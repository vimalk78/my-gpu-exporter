package config

import (
	"flag"
	"time"
)

// Config holds all configuration for the exporter
type Config struct {
	// DCGM
	DCGMUpdateFrequency time.Duration

	// Process Discovery
	ProcessScanInterval time.Duration

	// Kubernetes
	KubernetesEnabled  bool
	PodResourcesSocket string

	// Metrics
	MetricRetention time.Duration
	MetricPrefix    string

	// Energy Estimation
	EnableEnergyEstimation bool    // Enable SM-based energy estimation for time-slicing
	GPUIdlePower           float64 // GPU idle power in Watts (subtracted before attribution)

	// Server
	ListenAddress string
	MetricsPath   string

	// Logging
	LogLevel string
}

// NewConfig creates a new configuration with defaults
func NewConfig() *Config {
	return &Config{
		DCGMUpdateFrequency:    1 * time.Second,
		ProcessScanInterval:    10 * time.Second,
		KubernetesEnabled:      true,
		PodResourcesSocket:     "/var/lib/kubelet/pod-resources/kubelet.sock",
		MetricRetention:        5 * time.Minute,
		MetricPrefix:           "my_gpu_process",
		EnableEnergyEstimation: true, // Enabled by default for time-slicing support
		GPUIdlePower:           0,    // Default 0 = no idle power subtraction
		ListenAddress:          ":9400",
		MetricsPath:            "/metrics",
		LogLevel:               "info",
	}
}

// LoadFromFlags loads configuration from command-line flags
func (c *Config) LoadFromFlags() {
	flag.DurationVar(&c.DCGMUpdateFrequency, "dcgm-update-frequency", c.DCGMUpdateFrequency,
		"DCGM sampling frequency")

	flag.DurationVar(&c.ProcessScanInterval, "process-scan-interval", c.ProcessScanInterval,
		"How often to scan for new GPU processes")

	flag.BoolVar(&c.KubernetesEnabled, "kubernetes-enabled", c.KubernetesEnabled,
		"Enable Kubernetes pod mapping")

	flag.StringVar(&c.PodResourcesSocket, "pod-resources-socket", c.PodResourcesSocket,
		"Path to kubelet pod-resources socket")

	flag.DurationVar(&c.MetricRetention, "metric-retention", c.MetricRetention,
		"How long to retain metrics for exited processes")

	flag.StringVar(&c.MetricPrefix, "metric-prefix", c.MetricPrefix,
		"Prefix for Prometheus metric names")

	flag.BoolVar(&c.EnableEnergyEstimation, "enable-energy-estimation", c.EnableEnergyEstimation,
		"Enable SM-based energy estimation when time-slicing is detected")

	flag.Float64Var(&c.GPUIdlePower, "gpu-idle-power", c.GPUIdlePower,
		"GPU idle power in Watts (subtracted before per-process attribution)")

	flag.StringVar(&c.ListenAddress, "listen-address", c.ListenAddress,
		"Address to listen on for HTTP requests")

	flag.StringVar(&c.MetricsPath, "metrics-path", c.MetricsPath,
		"Path under which to expose metrics")

	flag.StringVar(&c.LogLevel, "log-level", c.LogLevel,
		"Log level (debug, info, warn, error)")

	flag.Parse()
}
