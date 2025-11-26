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

	// Per-process metric descriptors
	energyDesc         *prometheus.Desc
	smUtilDesc         *prometheus.Desc
	memUtilDesc        *prometheus.Desc
	memoryUsedDesc     *prometheus.Desc
	startTimeDesc      *prometheus.Desc
	activeDesc         *prometheus.Desc

	// GPU-level aggregation metrics (for time-slicing validation)
	gpuEnergyTotalDesc *prometheus.Desc
	gpuProcessCountDesc *prometheus.Desc
}

// NewExporter creates a new Prometheus exporter
func NewExporter(cfg *config.Config, col *collector.Collector) *Exporter {
	prefix := cfg.MetricPrefix

	// Common labels for all metrics
	// Use exported_ prefix for pod/namespace/container to match DCGM convention
	labels := []string{"pid", "gpu", "process_name", "exported_pod", "exported_namespace", "exported_container", "container_id"}

	// Energy metric has additional label to indicate if estimated
	energyLabels := append(labels, "energy_estimated")

	return &Exporter{
		config:    cfg,
		collector: col,

		// Energy metric - may be measured or estimated (indicated by label)
		energyDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_energy_joules_total", prefix),
			"Cumulative energy consumed by process in Joules (energy_estimated=true if SM-based estimation used)",
			energyLabels,
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

		// GPU-level aggregation metrics
		gpuEnergyTotalDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_gpu_energy_joules_total", prefix),
			"Total energy consumed by all processes on this GPU (sum of per-process energy)",
			[]string{"gpu"},
			nil,
		),

		gpuProcessCountDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_gpu_process_count", prefix),
			"Number of active processes on this GPU (indicates time-slicing when > 1)",
			[]string{"gpu"},
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
	ch <- e.gpuEnergyTotalDesc
	ch <- e.gpuProcessCountDesc
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
		// Include energy_estimated label to indicate if value is estimated or measured
		estimatedLabel := "false"
		if pm.EnergyEstimated {
			estimatedLabel = "true"
		}
		energyLabels := append(labels, estimatedLabel)

		ch <- prometheus.MustNewConstMetric(
			e.energyDesc,
			prometheus.CounterValue,
			pm.EnergyJoules,
			energyLabels...,
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

	// Export GPU-level aggregation metrics (for time-slicing validation)
	e.exportGPUAggregations(ch, metrics)
}

// exportGPUAggregations exports aggregated metrics per GPU
func (e *Exporter) exportGPUAggregations(ch chan<- prometheus.Metric, metrics map[uint]*collector.ProcessMetrics) {
	// Aggregate energy and count processes per GPU
	gpuEnergy := make(map[uint]float64)
	gpuProcessCount := make(map[uint]int)

	for _, pm := range metrics {
		if pm.IsRunning {
			gpuEnergy[pm.GPU] += pm.EnergyJoules
			gpuProcessCount[pm.GPU]++
		}
	}

	// Export aggregated metrics
	for gpuID, totalEnergy := range gpuEnergy {
		ch <- prometheus.MustNewConstMetric(
			e.gpuEnergyTotalDesc,
			prometheus.CounterValue,
			totalEnergy,
			fmt.Sprintf("%d", gpuID),
		)
	}

	for gpuID, processCount := range gpuProcessCount {
		ch <- prometheus.MustNewConstMetric(
			e.gpuProcessCountDesc,
			prometheus.GaugeValue,
			float64(processCount),
			fmt.Sprintf("%d", gpuID),
		)
	}
}
