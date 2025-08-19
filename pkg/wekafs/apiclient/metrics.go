package apiclient

import (
	"github.com/prometheus/client_golang/prometheus"
)

type ApiMetrics struct {
	client           *ApiClient
	Endpoints        *prometheus.GaugeVec
	requestCounters  *prometheus.CounterVec
	requestDurations *prometheus.HistogramVec
}

var GlobalApiMetrics *ApiMetrics

func NewApiMetrics(client *ApiClient) *ApiMetrics {
	if GlobalApiMetrics != nil {
		return GlobalApiMetrics
	}
	ret := &ApiMetrics{
		client: client,
		Endpoints: prometheus.NewGaugeVec(
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
	}
	ret.Init()
	GlobalApiMetrics = ret
	return ret
}

func (m *ApiMetrics) Init() {
	prometheus.MustRegister(m.Endpoints)
	prometheus.MustRegister(m.requestCounters)
	prometheus.MustRegister(m.requestDurations)
}
