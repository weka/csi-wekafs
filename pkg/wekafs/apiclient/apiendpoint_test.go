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

// TestNewApiClient_AllInvalidEndpoints_ReturnsError documents the current contract: NewApiClient used
// to succeed even when every credential endpoint failed validation, silently constructing a client
// with an empty endpoint map that only failed later - obscurely - at request time (a nil endpoint
// dereferenced in getBaseUrl and in do()'s stats bookkeeping, which used to panic before that was
// fixed separately). Failing loudly at construction time is more useful: there is no plausible way
// to recover a working client from a credential list where nothing parsed.
func TestNewApiClient_AllInvalidEndpoints_ReturnsError(t *testing.T) {
	quietLogs(t)
	creds := Credentials{
		Username:     "admin",
		Password:     "admin",
		Organization: "Root",
		HttpScheme:   "http",
		Endpoints:    []string{"not a valid host!!"},
	}
	c, err := NewApiClient(context.Background(), creds, ApiClientOptions{Hostname: "no-endpoints-test"})
	if err == nil {
		t.Fatal("expected NewApiClient to return an error when every endpoint fails validation")
	}
	if c != nil {
		t.Fatalf("expected a nil client alongside the error, got %v", c)
	}
	if _, ok := err.(*ApiNoEndpointsError); !ok {
		t.Fatalf("expected *ApiNoEndpointsError, got %T: %v", err, err)
	}
}

// TestConstructEndpointFromAddress covers the parsing rules constructEndpointFromAddress applies to a
// single raw credential endpoint: rejecting a URL scheme, defaulting the port, rejoining IPv6
// addresses split on ":", and rejecting anything that isn't a valid IPv4/IPv6 address or hostname.
func TestConstructEndpointFromAddress(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantIP   string
		wantPort int
		wantErr  bool
	}{
		{name: "ipv4 without port", input: "192.168.1.1", wantIP: "192.168.1.1", wantPort: 14000},
		{name: "ipv4 with port", input: "192.168.1.1:1234", wantIP: "192.168.1.1", wantPort: 1234},
		{name: "ipv6 with port", input: "::1:1234", wantIP: "::1", wantPort: 1234},
		{name: "hostname", input: "weka.example.com", wantIP: "weka.example.com", wantPort: 14000},
		{name: "https scheme rejected", input: "https://192.168.1.1:1234", wantErr: true},
		// Without the explicit scheme check this one slips through: splitting on ":" leaves "https"
		// as the host - which passes isValidHostname - and "//weka.example.com" as the port, which
		// fails to parse and falls back to the default. The result is a bogus "https:14000"
		// endpoint accepted in silence, so this case is what makes the scheme check load-bearing.
		{name: "scheme without port rejected", input: "https://weka.example.com", wantErr: true},
		{name: "garbage rejected", input: "not a valid host!!", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quietLogs(t)
			ep, err := constructEndpointFromAddress(context.Background(), tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error for input %q, got endpoint %v", tt.input, ep)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tt.input, err)
			}
			if ep.IpAddress != tt.wantIP || ep.MgmtPort != tt.wantPort {
				t.Fatalf("input %q: expected %s:%d, got %s:%d", tt.input, tt.wantIP, tt.wantPort, ep.IpAddress, ep.MgmtPort)
			}
		})
	}
}

// TestResetDefaultEndpoints_AllInvalid_ReturnsError covers the new contract: a credential list where
// every endpoint fails to parse must not silently produce an empty-but-successful endpoint set.
func TestResetDefaultEndpoints_AllInvalid_ReturnsError(t *testing.T) {
	quietLogs(t)
	a := &ApiClient{
		Credentials:  Credentials{Endpoints: []string{"not a valid host!!", "https://also-bad:1234"}},
		apiEndpoints: NewApiEndPoints(),
	}
	if err := a.resetDefaultEndpoints(context.Background()); err == nil {
		t.Fatal("expected an error when every endpoint fails to parse")
	}
	if got := a.apiEndpoints.Len(); got != 0 {
		t.Fatalf("expected no endpoints to be kept, got %d", got)
	}
}

// TestResetDefaultEndpoints_MixedValidity_KeepsOnlyTheGoodOnes covers the same contract on a mixed
// list: the bad entry is skipped and logged, not fatal, as long as at least one entry parses.
func TestResetDefaultEndpoints_MixedValidity_KeepsOnlyTheGoodOnes(t *testing.T) {
	quietLogs(t)
	a := &ApiClient{
		Credentials:  Credentials{Endpoints: []string{"not a valid host!!", "192.168.1.1:1234"}},
		apiEndpoints: NewApiEndPoints(),
	}
	if err := a.resetDefaultEndpoints(context.Background()); err != nil {
		t.Fatalf("expected no error when at least one endpoint parses, got %v", err)
	}
	if got := a.apiEndpoints.Len(); got != 1 {
		t.Fatalf("expected exactly the one valid endpoint to be kept, got %d", got)
	}
}
