package apiclient

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"
)

// ApiEndPoint describes a single management endpoint and accumulates per-endpoint request stats.
//
// The counters are atomic because every concurrent request increments them on whichever endpoint is
// currently selected, and they are shared, not copied: an ApiEndPoint must always be passed by
// pointer.
type ApiEndPoint struct {
	IpAddress            string
	MgmtPort             int
	failCount            atomic.Int64
	timeoutCount         atomic.Int64
	http400ErrCount      atomic.Int64
	http401ErrCount      atomic.Int64
	http403ErrCount      atomic.Int64
	http404ErrCount      atomic.Int64
	http409ErrCount      atomic.Int64
	http500ErrCount      atomic.Int64
	http503ErrCount      atomic.Int64
	generalErrCount      atomic.Int64
	transportErrCount    atomic.Int64
	noRespCount          atomic.Int64
	parseErrCount        atomic.Int64
	requestCount         atomic.Int64
	requestDurationTotal atomic.Int64 // nanoseconds
}

func (e *ApiEndPoint) String() string {
	return fmt.Sprintf("%s:%d", e.IpAddress, e.MgmtPort)
}

// ApiEndPoints holds the API endpoints known for a cluster and the one currently in use.
//
// Both are read on every request and replaced whenever the cluster's management addresses are
// refetched, which happens during login while other requests are in flight - so the state lives
// behind a lock rather than as loose fields on ApiClient.
type ApiEndPoints struct {
	sync.RWMutex
	currentEndpoint *ApiEndPoint
	endpoints       map[string]*ApiEndPoint
}

func NewApiEndPoints() *ApiEndPoints {
	return &ApiEndPoints{endpoints: make(map[string]*ApiEndPoint)}
}

// Replace swaps in a freshly discovered set of endpoints, keeping the current one selected if it
// still exists so an in-flight rotation is not lost.
func (eps *ApiEndPoints) Replace(endpoints map[string]*ApiEndPoint) {
	eps.Lock()
	defer eps.Unlock()
	eps.endpoints = endpoints
	if eps.currentEndpoint != nil {
		if _, stillThere := endpoints[eps.currentEndpoint.String()]; stillThere {
			return
		}
	}
	eps.currentEndpoint = nil
}

// Len reports how many endpoints are known.
func (eps *ApiEndPoints) Len() int {
	eps.RLock()
	defer eps.RUnlock()
	return len(eps.endpoints)
}

// Snapshot returns a copy of the endpoint map, so a caller can rebuild the set without holding the
// lock while another goroutine replaces the map. The ApiEndPoint values are deliberately shared, not
// copied: carrying the same objects forward is what preserves their accumulated stats, and their
// counters are atomic precisely so they can be shared.
func (eps *ApiEndPoints) Snapshot() map[string]*ApiEndPoint {
	eps.RLock()
	defer eps.RUnlock()
	out := make(map[string]*ApiEndPoint, len(eps.endpoints))
	for k, v := range eps.endpoints {
		out[k] = v
	}
	return out
}

// Keys returns the endpoint keys as a snapshot. Returning the map itself would let a caller iterate
// it after the lock is released, while another goroutine replaces it.
func (eps *ApiEndPoints) Keys() []string {
	eps.RLock()
	defer eps.RUnlock()
	out := make([]string, 0, len(eps.endpoints))
	for k := range eps.endpoints {
		out = append(out, k)
	}
	return out
}

// Rotate picks a different endpoint at random, excluding the current selection whenever more than
// one endpoint is known - callers rotate specifically to move off an endpoint that just failed, so
// re-selecting it defeats the purpose. It returns the endpoint it picked, or nil when none are
// known, so a caller that rotates to escape a failure doesn't have to re-read the selection
// afterwards (and risk logging one it never actually chose).
func (eps *ApiEndPoints) Rotate() *ApiEndPoint {
	eps.Lock()
	defer eps.Unlock()
	if len(eps.endpoints) == 0 {
		eps.currentEndpoint = nil
		return nil
	}
	keys := make([]string, 0, len(eps.endpoints))
	for k, v := range eps.endpoints {
		if v == eps.currentEndpoint {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		// Either there is only one endpoint, or (defensively) every entry aliases the current
		// pointer. Either way, fall back to the full set rather than the current-exclusion leaving
		// nothing to pick from.
		for k := range eps.endpoints {
			keys = append(keys, k)
		}
	}
	eps.currentEndpoint = eps.endpoints[keys[rand.Intn(len(keys))]]
	return eps.currentEndpoint
}

// Current returns the endpoint to use, selecting one if none is chosen yet. It returns nil when no
// endpoints are known rather than waiting for one to appear.
func (eps *ApiEndPoints) Current() *ApiEndPoint {
	eps.RLock()
	current := eps.currentEndpoint
	eps.RUnlock()
	if current != nil {
		return current
	}
	return eps.Rotate()
}

// constructEndpointFromAddress parses a single raw credential endpoint (e.g. "1.2.3.4",
// "1.2.3.4:1234", "[::1]:1234" or a hostname) into an ApiEndPoint. It returns an error rather than a
// nil endpoint so callers can log and skip the specific offending value instead of silently ending up
// with fewer endpoints than the credentials listed.
func constructEndpointFromAddress(ctx context.Context, e string) (*ApiEndPoint, error) {
	if strings.Contains(e, "://") {
		return nil, fmt.Errorf("endpoint must not include a URL scheme: %s", e)
	}

	split := strings.Split(e, ":")
	ip := ""
	port := "14000" // default port

	// if there is a port number in the endpoint, use it
	if len(split) > 1 {
		port = split[len(split)-1]
		ip = strings.Join(split[:len(split)-1], ":")
	} else {
		ip = split[0]
	}

	if !isValidIPv4Address(ip) && !isValidIPv6Address(ip) && !isValidHostname(ip) {
		log.Ctx(ctx).Error().Str("ip", ip).Str("port", port).Str("raw_endpoint", e).
			Msg("Cannot determine a valid hostname, IPv4 or IPv6 address, skipping endpoint")
		return nil, fmt.Errorf("cannot determine a valid hostname, IPv4 or IPv6 address for endpoint: %s", e)
	}

	portNum, err := strconv.Atoi(port)
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Str("port", port).Msg("Failed to parse port number, using default")
		portNum = 14000
	}
	return &ApiEndPoint{IpAddress: ip, MgmtPort: portNum}, nil
}

func (a *ApiClient) resetDefaultEndpoints(ctx context.Context) error {
	actualEndPoints := make(map[string]*ApiEndPoint)
	for _, e := range a.Credentials.Endpoints {
		endPoint, err := constructEndpointFromAddress(ctx, e)
		if err != nil {
			log.Ctx(ctx).Warn().Str("raw_endpoint", e).Err(err).Msg("Skipping invalid API endpoint")
			continue
		}
		// Key by the same "ip:port" form ApiEndPoint.String() and UpdateApiEndpoints use, not the
		// raw credential string: the raw string may carry no port (or a hostname alias) and would
		// then never match eps.currentEndpoint.String() in Replace, nor existingEndpoints[key] in
		// UpdateApiEndpoints - silently dropping the current selection and the accumulated counters
		// on every reset.
		actualEndPoints[endPoint.String()] = endPoint
	}
	// Report the failure before replacing, so a caller holding a working endpoint set is not left
	// with an empty one because the credentials it was re-reading turned out to be malformed.
	if len(actualEndPoints) == 0 {
		return errors.New("no valid API endpoints found")
	}
	a.apiEndpoints.Replace(actualEndPoints)
	return nil
}

// fetchMountEndpoints used to obtain actual data plane IP addresses
func (a *ApiClient) fetchMountEndpoints(ctx context.Context) error {
	log.Ctx(ctx).Trace().Msg("Fetching mount endpoints")
	a.MountEndpoints = []string{}
	nodes := &[]WekaNode{}
	err := a.GetNodesByRole(ctx, NodeRoleBackend, nodes)
	if err != nil {
		return err
	}
	for _, n := range *nodes {
		for _, i := range n.Ips {
			a.MountEndpoints = append(a.MountEndpoints, i)
		}
	}
	return nil
}

// UpdateApiEndpoints fetches current management IP addresses of the cluster
func (a *ApiClient) UpdateApiEndpoints(ctx context.Context) error {
	logger := log.Ctx(ctx)
	nodes := &[]WekaNode{}
	err := a.GetNodesByRole(ctx, NodeRoleManagement, nodes)
	if err != nil {
		return err
	}
	if len(*nodes) == 0 {
		logger.Error().Msg("No management nodes found, not updating endpoints")
		return errors.New("no management nodes found, could not update api endpoints")
	}

	newEndpoints := make(map[string]*ApiEndPoint)

	existingEndpoints := a.apiEndpoints.Snapshot()

	for _, n := range *nodes {
		// Make sure that only backends and not clients are added to the list
		if n.Mode == NodeModeBackend {
			for _, IpAddress := range n.Ips {
				endpointKey := fmt.Sprintf("%s:%d", IpAddress, n.MgmtPort)
				existingEndpoint, ok := existingEndpoints[endpointKey]
				if ok {
					logger.Debug().Str("endpoint", endpointKey).Msg("Updating existing API endpoint")
					newEndpoints[endpointKey] = existingEndpoint
				} else {
					logger.Info().Str("endpoint", endpointKey).Msg("Adding new API endpoint")
					endpoint := &ApiEndPoint{IpAddress: IpAddress, MgmtPort: n.MgmtPort}
					newEndpoints[endpointKey] = endpoint
				}
			}
		}
	}
	// Endpoints no longer present on the cluster are dropped simply by not being carried over:
	// newEndpoints is built from what discovery just returned, so anything missing from it is gone.
	for key := range existingEndpoints {
		if _, stillPresent := newEndpoints[key]; !stillPresent {
			logger.Warn().Str("endpoint", key).Msg("Removing inactive API endpoint")
		}
	}

	if len(newEndpoints) == 0 {
		return errors.New("no valid API endpoints found, keeping the existing endpoint set")
	}

	a.apiEndpoints.Replace(newEndpoints)

	// always rotate endpoint to make sure we distribute load between different Weka Nodes
	a.rotateEndpoint(ctx)
	return nil
}

// rotateEndpoint switches to a random endpoint other than the one currently in use.
func (a *ApiClient) rotateEndpoint(ctx context.Context) {
	logger := log.Ctx(ctx)
	if a.apiEndpoints.Len() == 0 {
		// Only reachable once the set is already empty, so a failure here leaves nothing worse
		// behind - Rotate below reports it as no endpoint to switch to.
		_ = a.resetDefaultEndpoints(ctx)
	}
	current := a.apiEndpoints.Rotate()
	if current == nil {
		logger.Error().Msg("Failed to choose random endpoint, no endpoints exist")
		return
	}
	logger.Debug().Str("new_endpoint", current.String()).Msg("Switched to new API endpoint")
}

// getEndpoint returns last known endpoint to work against. It returns nil when the credentials
// contain no endpoint that survived validation in resetDefaultEndpoints - callers that would
// otherwise dereference the result directly should go through requireEndpoint instead.
func (a *ApiClient) getEndpoint(ctx context.Context) *ApiEndPoint {
	if current := a.apiEndpoints.Current(); current != nil {
		return current
	}
	// Same as in rotateEndpoint: the set is already empty, so a failure changes nothing and the
	// nil return below is the signal callers act on.
	_ = a.resetDefaultEndpoints(ctx)
	return a.apiEndpoints.Current()
}

// requireEndpoint returns the endpoint to use, or ApiNoEndpointsError when none is known.
// NewApiClient now rejects credentials whose entries all fail validation, so a live client should
// always hold at least one endpoint; this stays as the guard for the case where the set is emptied
// later - every caller that would otherwise dereference getEndpoint's result directly goes
// through it rather than risking a nil dereference.
func (a *ApiClient) requireEndpoint(ctx context.Context) (*ApiEndPoint, apiError) {
	endpoint := a.getEndpoint(ctx)
	if endpoint == nil {
		return nil, &ApiNoEndpointsError{Err: errors.New("no endpoints could be found for API client")}
	}
	return endpoint, nil
}
