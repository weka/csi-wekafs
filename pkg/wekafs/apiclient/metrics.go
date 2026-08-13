package apiclient

import (
	"regexp"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricsNamespace = "weka_csi"
	metricsSubsystem = "api"
)

// apiMetricLabels are shared by every API request metric. Each is bounded: the driver name and
// cluster guid are fixed per process, the endpoint is one of a handful of management addresses, and
// status is one of the constants set in do(). The url label carries a generalised path rather than
// the request path, since raw paths embed filesystem uids and would grow a new time series per
// object - see generalizeUrlPathForMetrics.
var apiMetricLabels = []string{"csi_driver_name", "cluster_guid", "endpoint", "method", "url", "status"}

// ApiMetrics are the Prometheus metrics recorded for calls to the Weka API.
type ApiMetrics struct {
	requestCounters  *prometheus.CounterVec
	requestDurations *prometheus.HistogramVec
}

// apiMetrics is process-wide because the metrics describe traffic to the Weka API as a whole, and
// the cluster is already a label. Constructing the collectors has no side effect; they are only
// exported once something registers them, which is what Collectors is for.
var apiMetrics = newApiMetrics()

func newApiMetrics() *ApiMetrics {
	return &ApiMetrics{
		requestCounters: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: metricsNamespace,
				Subsystem: metricsSubsystem,
				Name:      "request_count",
				Help:      "Total number of API requests, by endpoint, method, url and status",
			},
			apiMetricLabels,
		),
		requestDurations: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: metricsNamespace,
				Subsystem: metricsSubsystem,
				Name:      "request_duration_seconds",
				Help:      "Duration of API requests in seconds, by endpoint, method, url and status",
				Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 7.5, 10, 15, 30, 60, 120, 300},
			},
			apiMetricLabels,
		),
	}
}

// Collectors returns the API metrics for the caller to register.
//
// Registration is deliberately the caller's decision rather than an init() that registers with the
// default registry: a package that registers on import cannot be used with a custom registry, and
// gives a test no way to start from a clean one.
func Collectors() []prometheus.Collector {
	return []prometheus.Collector{apiMetrics.requestCounters, apiMetrics.requestDurations}
}

var (
	// Matches a UUID anywhere in the path, e.g. filesystems/<uid>/quota.
	uuidInPath = regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`)
	// Matches a path segment that is entirely digits, e.g. an inode id.
	numericPathSegment = regexp.MustCompile(`\b\d+\b`)
)

// generalizeUrlPathForMetrics turns a request path into the shape of the request, so that calls
// against different objects share one time series. Without it every filesystem, snapshot and inode
// would produce its own series and the metric's cardinality would track the number of objects on
// the cluster.
func generalizeUrlPathForMetrics(urlPath string) string {
	path := uuidInPath.ReplaceAllString(urlPath, "{guid}")
	path = numericPathSegment.ReplaceAllString(path, "{id}")
	return strings.TrimSuffix(path, "/")
}
