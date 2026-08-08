package apiclient

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient/apiclienttest"
)

func TestGeneralizeUrlPathForMetrics(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"filesystems", "filesystems"},
		{
			"filesystems/8c1a5d4e-1b2f-4c3a-9d5e-6f7a8b9c0d1e/quota",
			"filesystems/{guid}/quota",
		},
		{
			"FILESYSTEMS/8C1A5D4E-1B2F-4C3A-9D5E-6F7A8B9C0D1E/quota",
			"FILESYSTEMS/{guid}/quota",
		},
		{"fileSystems/1234/snapshots", "fileSystems/{id}/snapshots"},
		{"cluster/", "cluster"},
		// A version segment is not a bare number, so it must survive intact - otherwise every
		// request would collapse onto the same series.
		{"api/v2/cluster", "api/v2/cluster"},
		{
			"filesystems/8c1a5d4e-1b2f-4c3a-9d5e-6f7a8b9c0d1e/quotas/42",
			"filesystems/{guid}/quotas/{id}",
		},
	} {
		if got := generalizeUrlPathForMetrics(tc.in); got != tc.want {
			t.Errorf("generalizeUrlPathForMetrics(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The whole point of generalizing the path is that the number of series must not grow with the
// number of objects on the cluster.
func TestGeneralizeUrlPathBoundsCardinality(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		path := "filesystems/" + uuid.New().String() + "/quota"
		seen[generalizeUrlPathForMetrics(path)] = struct{}{}
	}
	if len(seen) != 1 {
		t.Errorf("100 filesystems produced %d distinct label values, want 1: %v", len(seen), seen)
	}
}

func TestCollectorsAreNotRegisteredOnImport(t *testing.T) {
	// Registering into a fresh registry must succeed, which it cannot if the package already
	// registered these collectors somewhere on import.
	reg := prometheus.NewRegistry()
	if err := reg.Register(apiMetrics.requestCounters); err != nil {
		t.Fatalf("request counters were already registered elsewhere: %v", err)
	}
	if got := len(Collectors()); got != 2 {
		t.Errorf("Collectors() returned %d collectors, want 2", got)
	}
}

// End to end: drive a real client against the fake API and check the request metrics are recorded
// with the labels we expect.
//
// apiMetrics is process-wide, so other tests in this package record into the same collectors. The
// assertions therefore select this test's own series by a driver name nothing else uses, rather
// than looking at the metric as a whole.
func TestApiRequestMetricsAreRecorded(t *testing.T) {
	quietLogs(t)
	const driverName = "csi.weka.io.metrics-test"
	server := apiclienttest.New(t)

	client, err := NewApiClient(context.Background(), Credentials{
		Username:     "admin",
		Password:     "password",
		Organization: "Root",
		HttpScheme:   "http",
		Endpoints:    []string{server.Addr()},
	}, ApiClientOptions{AllowInsecureHttps: true, Hostname: "test", DriverName: driverName})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	if err := client.Login(context.Background()); err != nil {
		t.Fatalf("failed to log in: %v", err)
	}

	var found bool
	for _, mf := range gather(t) {
		if mf.GetName() != "weka_csi_api_request_count" {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["csi_driver_name"] != driverName || labels["url"] != "login" {
				continue
			}
			found = true
			if labels["method"] != "POST" {
				t.Errorf("method = %q, want POST", labels["method"])
			}
			if !strings.HasPrefix(labels["status"], "http_") {
				t.Errorf("status = %q, want an http_ status for a completed request", labels["status"])
			}
			if labels["endpoint"] == "" {
				t.Error("endpoint label is empty")
			}
			if got := m.GetCounter().GetValue(); got < 1 {
				t.Errorf("counter = %v, want at least 1", got)
			}
		}
	}
	if !found {
		t.Errorf("no request_count series recorded for the login call of driver %q", driverName)
	}
}

func gather(t *testing.T) []*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(apiMetrics.requestCounters); err != nil {
		t.Fatalf("failed to register counters: %v", err)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather: %v", err)
	}
	return mfs
}
