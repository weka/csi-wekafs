package apiclient

import (
	"context"
	"testing"
	"time"
)

// newClientWithEndpoints builds a client over two known endpoints without contacting anything.
func newClientWithEndpoints(t *testing.T, rotate bool) *ApiClient {
	t.Helper()
	a, err := NewApiClient(context.Background(), Credentials{
		Username:     "admin",
		Password:     "admin",
		Organization: "Root",
		HttpScheme:   "http",
		Endpoints:    []string{"127.0.0.1:14000", "127.0.0.2:14000"},
	}, ApiClientOptions{
		Hostname:                    "rotate-test",
		RotateEndpointOnEachRequest: rotate,
		// Nothing is listening on these addresses. Without a short timeout each attempt would wait
		// out the 60s default, and the test would take minutes to make its point.
		ApiTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to build client: %v", err)
	}
	if a.apiEndpoints.Len() != 2 {
		t.Fatalf("expected both endpoints to be usable, got %d", a.apiEndpoints.Len())
	}
	return a
}

// TestRotateEndpointOnEachRequest covers the metrics server's traffic pattern: a steady stream of
// read-only calls should be spread over the management nodes rather than all landing on one. The
// requests themselves fail (nothing is listening), which is irrelevant - rotation happens before
// the request is sent, so the selected endpoint is what this asserts on.
func TestRotateEndpointOnEachRequest(t *testing.T) {
	quietLogs(t)
	a := newClientWithEndpoints(t, true)

	first := a.getEndpoint(context.Background()).String()
	seen := map[string]bool{first: true}
	for i := 0; i < 4; i++ {
		_, _ = a.do(context.Background(), "GET", "cluster", nil, nil)
		seen[a.getEndpoint(context.Background()).String()] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected requests to be spread over both endpoints, only ever used %v", seen)
	}
}

// TestNoRotationWhenDisabled is the other half: the plugin must stay on one endpoint until it
// actually misbehaves, so its per-endpoint failure counters mean something.
func TestNoRotationWhenDisabled(t *testing.T) {
	quietLogs(t)
	a := newClientWithEndpoints(t, false)

	want := a.getEndpoint(context.Background()).String()
	for i := 0; i < 4; i++ {
		_, _ = a.do(context.Background(), "GET", "cluster", nil, nil)
		if got := a.getEndpoint(context.Background()).String(); got != want {
			t.Fatalf("iteration %d: endpoint moved from %s to %s with rotation disabled", i, want, got)
		}
	}
}
