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
// read-only calls should be spread over the management nodes rather than all landing on one.
// Asserted on the per-request rotation itself rather than through a request, because a request to
// an address nothing is listening on spends five retries getting there.
func TestRotateEndpointOnEachRequest(t *testing.T) {
	quietLogs(t)
	a := newClientWithEndpoints(t, true)

	seen := map[string]bool{a.getEndpoint(context.Background()).String(): true}
	for range 4 {
		a.rotateForNewRequest()
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
	for i := range 4 {
		a.rotateForNewRequest()
		if got := a.getEndpoint(context.Background()).String(); got != want {
			t.Fatalf("iteration %d: endpoint moved from %s to %s with rotation disabled", i, want, got)
		}
	}
}

// A retry must stay on the endpoint the failure handler moved to. do() used to rotate on every
// attempt, and Rotate excludes only the current selection - so with two endpoints the retry was
// sent straight back to the node that had just failed, every time, and failover never happened.
//
// This asserts the attempt itself does not rotate: the request below fails (nothing is listening),
// which is the point - what matters is where the next attempt would be sent.
func TestRetryStaysOffTheFailedEndpoint(t *testing.T) {
	quietLogs(t)
	a := newClientWithEndpoints(t, true)
	ctx := context.Background()

	failed := a.getEndpoint(ctx).String()
	// What the transient-error path in request() does once an attempt fails.
	a.rotateEndpoint(ctx)
	afterFailover := a.getEndpoint(ctx).String()
	if afterFailover == failed {
		t.Fatalf("rotateEndpoint stayed on the failed endpoint %s", failed)
	}

	_, _ = a.do(ctx, "GET", "cluster", nil, nil)

	if got := a.getEndpoint(ctx).String(); got != afterFailover {
		t.Errorf("the attempt moved the endpoint from %s to %s; a retry would be sent back to the failed node %s",
			afterFailover, got, failed)
	}
}

// Everything in one attempt has to name the same endpoint: the URL it is sent to, the Prometheus
// label it is counted under, and the per-endpoint counters that decide which node looks healthy.
// Those were three separate reads of a shared selection that any concurrent rotation could move.
func TestAttemptResolvesOneEndpoint(t *testing.T) {
	quietLogs(t)
	a := newClientWithEndpoints(t, true)
	ctx := context.Background()

	chosen := a.getEndpoint(ctx)
	endpoints := a.apiEndpoints.Snapshot()
	before := map[string]int64{}
	for name, ep := range endpoints {
		before[name] = ep.requestCount.Load()
	}

	_, _ = a.do(ctx, "GET", "cluster", nil, nil)

	for name, ep := range endpoints {
		got := ep.requestCount.Load() - before[name]
		want := int64(0)
		if ep == chosen {
			want = 1
		}
		if got != want {
			t.Errorf("endpoint %s counted %d requests, want %d - the attempt was attributed to a node it was not sent to",
				name, got, want)
		}
	}
}
