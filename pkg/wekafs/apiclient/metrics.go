package apiclient

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

type ApiMetrics struct {
	endpoints        *prometheus.GaugeVec
	requestCounters  *prometheus.CounterVec
	requestDurations *prometheus.HistogramVec
	backendCalls     *prometheus.CounterVec
}

var apiMetrics *ApiMetrics

func init() {
	InitApiMetrics()
}

// InitApiMetrics initializes and registers API metrics with Prometheus.
func InitApiMetrics() {
	apiMetrics = &ApiMetrics{
		endpoints: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "weka_csi",
				Subsystem: "api",
				Name:      "endpoints_count",
				Help:      "Total number of API endpoints",
			},
			[]string{"csi_driver_name", "cluster_guid", "endpoint_status"},
		),
		requestCounters: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "weka_csi",
				Subsystem: "api",
				Name:      "request_count",
				Help:      "Total number of API requests broken down by endpoint, method, url, status",
			},
			[]string{"csi_driver_name", "cluster_guid", "endpoint", "method", "url", "status"},
		),
		requestDurations: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "weka_csi",
				Subsystem: "api",
				Name:      "request_duration_seconds",
				Help:      "Duration of API requests in seconds broken down by endpoint, method, url, status",
				Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 7.5, 10, 15, 30, 60, 120, 300},
			},
			[]string{"csi_driver_name", "cluster_guid", "endpoint", "method", "url", "status"},
		),
		backendCalls: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "weka_csi",
				Subsystem: "metricsserver",
				Name:      "backend_call",
				Help:      "Total number of backend API calls broken down by path, result, status code, and secret name",
			},
			[]string{"path", "result", "status_code", "secret_name"},
		),
	}

	prometheus.MustRegister(apiMetrics.requestCounters)
	prometheus.MustRegister(apiMetrics.requestDurations)
	prometheus.MustRegister(apiMetrics.backendCalls)
	log.Debug().Msg("API metrics registered with Prometheus")
}
