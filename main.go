package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/vimalk78/my-gpu-exporter/pkg/collector"
	"github.com/vimalk78/my-gpu-exporter/pkg/config"
	"github.com/vimalk78/my-gpu-exporter/pkg/exporter"
)

func main() {
	// Load configuration
	cfg := config.NewConfig()
	cfg.LoadFromFlags()

	// Setup logging
	setupLogging(cfg.LogLevel)

	slog.Info("Starting my-gpu-exporter",
		slog.String("version", "1.0.0"),
		slog.String("listen_address", cfg.ListenAddress),
		slog.String("metrics_path", cfg.MetricsPath))

	slog.Info("Configuration",
		slog.Duration("process_scan_interval", cfg.ProcessScanInterval),
		slog.Duration("metric_retention", cfg.MetricRetention),
		slog.Bool("kubernetes_enabled", cfg.KubernetesEnabled))

	// Create collector
	col, err := collector.NewCollector(cfg)
	if err != nil {
		slog.Error("Failed to create collector", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer col.Shutdown()

	// Create Prometheus exporter
	exp := exporter.NewExporter(cfg, col)

	// Register exporter
	prometheus.MustRegister(exp)

	slog.Info("Registered Prometheus exporter")

	// Setup HTTP server
	mux := http.NewServeMux()

	// Metrics endpoint
	mux.Handle(cfg.MetricsPath, promhttp.Handler())

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK\n")
	})

	// Root endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html>
<head><title>My GPU Exporter</title></head>
<body>
<h1>My GPU Exporter</h1>
<p>Per-process GPU energy metrics</p>
<ul>
<li><a href="%s">Metrics</a></li>
<li><a href="/health">Health</a></li>
</ul>
<p><strong>IMPORTANT:</strong> Energy values are ACTUAL hardware-measured values, NOT estimated.</p>
</body>
</html>`, cfg.MetricsPath)
	})

	server := &http.Server{
		Addr:         cfg.ListenAddress,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// Start server in goroutine
	go func() {
		slog.Info("Starting HTTP server", slog.String("address", cfg.ListenAddress))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	slog.Info("Exporter started successfully",
		slog.String("metrics_url", fmt.Sprintf("http://%s%s", cfg.ListenAddress, cfg.MetricsPath)))

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	slog.Info("Received shutdown signal", slog.String("signal", sig.String()))

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("HTTP server shutdown failed", slog.String("error", err.Error()))
	}

	slog.Info("Exporter stopped")
}

func setupLogging(level string) {
	var logLevel slog.Level

	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})

	slog.SetDefault(slog.New(handler))
}
