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

func NewApiMetrics(client *ApiClient) *ApiMetrics {
	ret := &ApiMetrics{
		client: client,
		Endpoints: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "weka_csi",
				Subsystem: "api",
				Name:      "endpoints_count",
				Help:      "Total number of API endpoints",
			},
			[]string{"cluster_guid", "endpoint_status"},
		),
		requestCounters: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "weka_csi",
				Subsystem: "api",
				Name:      "request_count",
				Help:      "Total number of API requests broken down by endpoint, method, url, status",
			},
			[]string{"cluster_guid", "endpoint", "method", "url", "status"},
		),
		requestDurations: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "weka_csi",
				Subsystem: "api",
				Name:      "request_duration_seconds",
				Help:      "Duration of API requests in seconds broken down by endpoint, method, url, status",
			},
			[]string{"cluster_guid", "endpoint", "method", "url", "status"},
		),
	}
	ret.Init()
	return ret
}

func (m *ApiMetrics) Init() {
	prometheus.MustRegister(m.Endpoints)
	prometheus.MustRegister(m.requestCounters)
	prometheus.MustRegister(m.requestDurations)
}
