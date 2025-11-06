package exporter

import (
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/vimalk78/my-gpu-exporter/pkg/collector"
	"github.com/vimalk78/my-gpu-exporter/pkg/config"
)

// Exporter implements prometheus.Collector
type Exporter struct {
	config    *config.Config
	collector *collector.Collector

	// Metric descriptors
	energyDesc         *prometheus.Desc
	smUtilDesc         *prometheus.Desc
	memUtilDesc        *prometheus.Desc
	memoryUsedDesc     *prometheus.Desc
	startTimeDesc      *prometheus.Desc
	activeDesc         *prometheus.Desc
}

// NewExporter creates a new Prometheus exporter
func NewExporter(cfg *config.Config, col *collector.Collector) *Exporter {
	prefix := cfg.MetricPrefix

	// Common labels for all metrics
	labels := []string{"pid", "gpu", "process_name", "pod", "namespace", "container", "container_id"}

	return &Exporter{
		config:    cfg,
		collector: col,

		// Energy metric - ACTUAL MEASURED VALUE (not estimated)
		energyDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_energy_joules_total", prefix),
			"ACTUAL hardware-measured cumulative energy consumed by process in Joules (NOT estimated)",
			labels,
			nil,
		),

		// Utilization metrics
		smUtilDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_sm_utilization_ratio", prefix),
			"SM (Streaming Multiprocessor) utilization ratio (0.0-1.0)",
			labels,
			nil,
		),

		memUtilDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_memory_utilization_ratio", prefix),
			"Memory utilization ratio (0.0-1.0)",
			labels,
			nil,
		),

		// Memory metrics
		memoryUsedDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_memory_used_bytes", prefix),
			"GPU memory used by process in bytes",
			labels,
			nil,
		),

		// Lifecycle metrics
		startTimeDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_start_time_seconds", prefix),
			"Process start time in seconds since epoch",
			labels,
			nil,
		),

		activeDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_active", prefix),
			"Process active status (1=running, 0=exited)",
			labels,
			nil,
		),
	}
}

// Describe implements prometheus.Collector
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.energyDesc
	ch <- e.smUtilDesc
	ch <- e.memUtilDesc
	ch <- e.memoryUsedDesc
	ch <- e.startTimeDesc
	ch <- e.activeDesc
}

// Collect implements prometheus.Collector
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	// Trigger collection
	if err := e.collector.Collect(); err != nil {
		slog.Error("Collection failed", slog.String("error", err.Error()))
		return
	}

	// Get metrics
	metrics := e.collector.GetMetrics()

	slog.Debug("Exporting metrics", slog.Int("process_count", len(metrics)))

	for _, pm := range metrics {
		// Build label values
		labels := []string{
			fmt.Sprintf("%d", pm.PID),
			fmt.Sprintf("%d", pm.GPU),
			pm.ProcessName,
			pm.PodName,
			pm.PodNamespace,
			pm.ContainerName,
			pm.ContainerID,
		}

		// Energy - COUNTER (cumulative)
		// This is ACTUAL measured energy from GPU hardware, NOT estimated
		ch <- prometheus.MustNewConstMetric(
			e.energyDesc,
			prometheus.CounterValue,
			pm.EnergyJoules,
			labels...,
		)

		// SM Utilization - GAUGE
		ch <- prometheus.MustNewConstMetric(
			e.smUtilDesc,
			prometheus.GaugeValue,
			pm.SmUtilization,
			labels...,
		)

		// Memory Utilization - GAUGE
		ch <- prometheus.MustNewConstMetric(
			e.memUtilDesc,
			prometheus.GaugeValue,
			pm.MemUtilization,
			labels...,
		)

		// Memory Used - GAUGE
		ch <- prometheus.MustNewConstMetric(
			e.memoryUsedDesc,
			prometheus.GaugeValue,
			float64(pm.MemoryUsedBytes),
			labels...,
		)

		// Start Time - GAUGE
		ch <- prometheus.MustNewConstMetric(
			e.startTimeDesc,
			prometheus.GaugeValue,
			float64(pm.StartTime.Unix()),
			labels...,
		)

		// Active Status - GAUGE
		activeValue := 0.0
		if pm.IsRunning {
			activeValue = 1.0
		}
		ch <- prometheus.MustNewConstMetric(
			e.activeDesc,
			prometheus.GaugeValue,
			activeValue,
			labels...,
		)
	}
}
