package apiclient

import (
	"context"
	"testing"
)

// TestResetDefaultEndpoints_KeysMatchEndpointString covers fix 2: resetDefaultEndpoints used to key
// its map by the raw credential string (which may carry no port), while Replace tests the current
// selection's membership with ApiEndPoint.String() ("ip:port"), and UpdateApiEndpoints looks up
// existing endpoints the same way. A raw endpoint with no explicit port is the case that most
// visibly breaks: its raw key ("127.0.0.1") never matches its own String() ("127.0.0.1:14000").
func TestResetDefaultEndpoints_KeysMatchEndpointString(t *testing.T) {
	a := &ApiClient{
		Credentials:  Credentials{Endpoints: []string{"127.0.0.1"}},
		apiEndpoints: NewApiEndPoints(),
	}
	a.resetDefaultEndpoints(context.Background())

	current := a.apiEndpoints.Current()
	if current == nil {
		t.Fatal("expected an endpoint to be selected after resetDefaultEndpoints")
	}

	snapshot := a.apiEndpoints.Snapshot()
	got, ok := snapshot[current.String()]
	if !ok {
		t.Fatalf("endpoint not found under its own String() key %q; map keys: %v", current.String(), keysOf(snapshot))
	}
	if got != current {
		t.Fatalf("expected the endpoint stored under key %q to be the selected endpoint", current.String())
	}
}

// TestUpdateApiEndpoints_ReusesDefaultEndpointObject covers the concrete consequence of fix 2:
// before it, a default endpoint minted by resetDefaultEndpoints (raw-string keyed) could never match
// existingEndpoints[endpointKey] in UpdateApiEndpoints (ip:port keyed), so rediscovering the very
// same address produced a brand new *ApiEndPoint and silently dropped its accumulated counters. This
// exercises the same map lookup UpdateApiEndpoints performs, without needing a live cluster to drive
// GetNodesByRole.
func TestUpdateApiEndpoints_ReusesDefaultEndpointObject(t *testing.T) {
	a := &ApiClient{
		Credentials:  Credentials{Endpoints: []string{"127.0.0.1"}}, // no explicit port
		apiEndpoints: NewApiEndPoints(),
	}
	a.resetDefaultEndpoints(context.Background())

	defaultEndpoint := a.apiEndpoints.Current()
	if defaultEndpoint == nil {
		t.Fatal("expected a default endpoint")
	}
	defaultEndpoint.requestCount.Add(7)
	defaultEndpoint.failCount.Add(3)

	// This mirrors UpdateApiEndpoints's own lookup: a freshly discovered node at the same
	// "ip:port" the default endpoint already uses should resolve to the very same object.
	existingEndpoints := a.apiEndpoints.Snapshot()
	endpointKey := defaultEndpoint.String()
	existingEndpoint, ok := existingEndpoints[endpointKey]
	if !ok {
		t.Fatalf("default endpoint not found under discovery's key format %q; map keys: %v", endpointKey, keysOf(existingEndpoints))
	}
	if existingEndpoint != defaultEndpoint {
		t.Fatalf("expected UpdateApiEndpoints's lookup to reuse the same *ApiEndPoint object")
	}
	if existingEndpoint.requestCount.Load() != 7 || existingEndpoint.failCount.Load() != 3 {
		t.Fatalf("expected accumulated counters to survive, got requestCount=%d failCount=%d",
			existingEndpoint.requestCount.Load(), existingEndpoint.failCount.Load())
	}
}

// TestApiEndPoints_ReplaceKeepsCurrentSelectionAcrossReset covers the other consequence named in fix
// 2: Replace only preserves the current selection when its String() key is present in the freshly
// discovered map. Before the fix, a reset that rediscovers the very same address produced a map
// keyed differently than the current selection expects, so the selection - and the object carrying
// its stats - was dropped every time.
func TestApiEndPoints_ReplaceKeepsCurrentSelectionAcrossReset(t *testing.T) {
	eps := NewApiEndPoints()
	first := &ApiEndPoint{IpAddress: "127.0.0.1", MgmtPort: 14000}
	eps.Replace(map[string]*ApiEndPoint{first.String(): first})
	if got := eps.Rotate(); got != first {
		t.Fatalf("expected the single known endpoint to be selected, got %v", got)
	}

	// A later rediscovery of the same address, as both resetDefaultEndpoints and UpdateApiEndpoints
	// perform it: a brand new map, keyed consistently with ApiEndPoint.String().
	second := &ApiEndPoint{IpAddress: "127.0.0.1", MgmtPort: 14000}
	eps.Replace(map[string]*ApiEndPoint{second.String(): second})

	if eps.Current() == nil {
		t.Fatal("expected the current selection to survive a reset that rediscovers the same address")
	}
}

// keysOf returns the keys of an endpoint map for use in test failure messages.
func keysOf(m map[string]*ApiEndPoint) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestApiEndPoints_RotateExcludesCurrentEndpoint covers fix 7: Rotate's doc promises a *different*
// endpoint, but it used to sample uniformly including the current one, so request()'s retry after a
// transient error - which calls rotateEndpoint specifically to move off a failing node - stayed on
// the dead endpoint about half the time with two known endpoints. 50 iterations makes a surviving
// bug's failure probability (0.5^50) indistinguishable from zero.
func TestApiEndPoints_RotateExcludesCurrentEndpoint(t *testing.T) {
	eps := NewApiEndPoints()
	epA := &ApiEndPoint{IpAddress: "10.0.0.1", MgmtPort: 14000}
	epB := &ApiEndPoint{IpAddress: "10.0.0.2", MgmtPort: 14000}
	eps.Replace(map[string]*ApiEndPoint{epA.String(): epA, epB.String(): epB})
	eps.currentEndpoint = epA

	for i := 0; i < 50; i++ {
		previous := eps.Current()
		next := eps.Rotate()
		if next == nil {
			t.Fatalf("iteration %d: expected a non-nil endpoint", i)
		}
		if next == previous {
			t.Fatalf("iteration %d: Rotate returned the current endpoint; with 2+ endpoints it must always move off it", i)
		}
	}
}

// TestApiEndPoints_RotateSingleEndpointIsStable checks that the fix 7 exclusion logic does not
// leave Rotate with nothing to pick from when only one endpoint is known.
func TestApiEndPoints_RotateSingleEndpointIsStable(t *testing.T) {
	eps := NewApiEndPoints()
	only := &ApiEndPoint{IpAddress: "10.0.0.1", MgmtPort: 14000}
	eps.Replace(map[string]*ApiEndPoint{only.String(): only})

	for i := 0; i < 5; i++ {
		if got := eps.Rotate(); got != only {
			t.Fatalf("iteration %d: expected the single known endpoint to be selected, got %v", i, got)
		}
	}
}

// TestNewApiClient_AllInvalidEndpoints_ReturnsErrorInsteadOfPanicking covers fix 8. NewApiClient only
// rejects a raw endpoint list with zero entries; a secret whose entries all fail
// isValidIPv4Address/isValidIPv6Address/isValidHostname still constructs a client, just with an
// empty endpoint map. Before the fix, the first request dereferenced that nil endpoint directly in
// getBaseUrl and in do()'s stats bookkeeping and panicked.
func TestNewApiClient_AllInvalidEndpoints_ReturnsErrorInsteadOfPanicking(t *testing.T) {
	quietLogs(t)
	creds := Credentials{
		Username:     "admin",
		Password:     "admin",
		Organization: "Root",
		HttpScheme:   "http",
		Endpoints:    []string{"not a valid host!!"},
	}
	c, err := NewApiClient(context.Background(), creds, ApiClientOptions{Hostname: "no-endpoints-test"})
	if err != nil {
		t.Fatalf("NewApiClient should succeed here - validation failure only empties the endpoint map, it doesn't reject the raw list: %v", err)
	}

	var gotErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Get panicked instead of returning an error: %v", r)
			}
		}()
		gotErr = c.Get(context.Background(), "cluster", nil, &ClusterInfoResponse{})
	}()

	if gotErr == nil {
		t.Fatal("expected an error when the client has no valid endpoints")
	}
	if _, ok := gotErr.(*ApiNoEndpointsError); !ok {
		t.Fatalf("expected *ApiNoEndpointsError, got %T: %v", gotErr, gotErr)
	}
}
