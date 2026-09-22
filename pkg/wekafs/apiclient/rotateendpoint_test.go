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

	ctx := context.Background()
	seen := map[string]bool{}
	for range 5 {
		ep, err := a.endpointForNewRequest(ctx)
		if err != nil {
			t.Fatalf("no endpoint for request: %v", err)
		}
		seen[ep.String()] = true
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

	ctx := context.Background()
	want := a.getEndpoint(ctx).String()
	for i := range 4 {
		ep, err := a.endpointForNewRequest(ctx)
		if err != nil {
			t.Fatalf("no endpoint for request: %v", err)
		}
		if got := ep.String(); got != want {
			t.Fatalf("iteration %d: endpoint moved from %s to %s with rotation disabled", i, want, got)
		}
	}
}

// A retry must stay off the endpoint that failed. do() used to rotate on every attempt, and the
// rotation excluded only the *shared* selection - so with two endpoints a retry went straight back
// to the node that had just failed, every time, and failover never happened.
func TestRetryStaysOffTheFailedEndpoint(t *testing.T) {
	quietLogs(t)
	a := newClientWithEndpoints(t, true)
	ctx := context.Background()

	failed, err := a.endpointForNewRequest(ctx)
	if err != nil {
		t.Fatalf("no endpoint for request: %v", err)
	}
	next := a.rotateEndpointFrom(ctx, failed)
	if next == nil || next == failed {
		t.Fatalf("failure handling stayed on the failed endpoint %s", failed)
	}
}

// And it must move off the endpoint *this* request used, not off whatever the shared selection
// holds by then: a concurrent request moves that, and excluding it would leave this request's
// failed node eligible for its own retry.
func TestFailureMovesOffThisRequestsEndpoint(t *testing.T) {
	quietLogs(t)
	a := newClientWithEndpoints(t, true)
	ctx := context.Background()

	failed, err := a.endpointForNewRequest(ctx)
	if err != nil {
		t.Fatalf("no endpoint for request: %v", err)
	}
	// Stand in for a concurrent request moving the shared selection back onto the failed node.
	for range 8 {
		if a.apiEndpoints.Current() == failed {
			break
		}
		a.apiEndpoints.Rotate()
	}
	if a.apiEndpoints.Current() != failed {
		t.Skip("could not steer the shared selection onto the failed endpoint")
	}

	if next := a.rotateEndpointFrom(ctx, failed); next == failed {
		t.Errorf("retry would be sent back to the failed endpoint %s", failed)
	}
}

// Everything in one attempt has to name the same endpoint: the URL it is sent to, the Prometheus
// label it is counted under, and the per-endpoint counters that decide which node looks healthy.
func TestAttemptUsesOnlyTheEndpointItWasGiven(t *testing.T) {
	quietLogs(t)
	a := newClientWithEndpoints(t, true)
	ctx := context.Background()

	endpoints := a.apiEndpoints.Snapshot()
	var chosen *ApiEndPoint
	for _, ep := range endpoints {
		chosen = ep
		break
	}
	// Stand in for a concurrent request having moved the shared selection elsewhere. An attempt
	// that read the selection back instead of using the endpoint it was handed would now send the
	// call to the wrong node - and, more to the point, credit the wrong node's failure counters.
	for range 8 {
		if a.apiEndpoints.Current() != chosen {
			break
		}
		a.apiEndpoints.Rotate()
	}
	if a.apiEndpoints.Current() == chosen {
		t.Skip("could not steer the shared selection off the chosen endpoint")
	}

	before := map[string]int64{}
	for name, ep := range endpoints {
		before[name] = ep.requestCount.Load()
	}

	_, _ = a.do(ctx, chosen, "GET", "cluster", nil, nil)

	for name, ep := range endpoints {
		got := ep.requestCount.Load() - before[name]
		want := int64(0)
		if ep == chosen {
			want = 1
		}
		if got != want {
			t.Errorf("endpoint %s counted %d requests, want %d - the attempt did not stay on the endpoint it was given",
				name, got, want)
		}
	}
}
