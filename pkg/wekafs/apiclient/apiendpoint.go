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
	"time"

	"github.com/rs/zerolog/log"
)

// ApiEndPoint describes a single management endpoint and accumulates per-endpoint request stats.
//
// The counters are atomic because every concurrent request increments them on whichever endpoint is
// currently selected, and they are shared, not copied: an ApiEndPoint must always be passed by
// pointer.
type ApiEndPoint struct {
	IpAddress string
	MgmtPort  int
	// lastActive records when the endpoint was first discovered. It is set at construction and never
	// mutated afterwards, so a rediscovered endpoint keeps its original object and its stats.
	lastActive           time.Time
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

// Rotate picks a different endpoint at random. It is a no-op when none are known.
func (eps *ApiEndPoints) Rotate() {
	eps.Lock()
	defer eps.Unlock()
	if len(eps.endpoints) == 0 {
		eps.currentEndpoint = nil
		return
	}
	keys := make([]string, 0, len(eps.endpoints))
	for k := range eps.endpoints {
		keys = append(keys, k)
	}
	eps.currentEndpoint = eps.endpoints[keys[rand.Intn(len(keys))]]
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
	eps.Rotate()
	eps.RLock()
	defer eps.RUnlock()
	return eps.currentEndpoint
}

func (a *ApiClient) resetDefaultEndpoints(ctx context.Context) {
	actualEndPoints := make(map[string]*ApiEndPoint)
	for _, e := range a.Credentials.Endpoints {

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
			continue
		}

		portNum, err := strconv.Atoi(port)
		if err != nil {
			log.Ctx(ctx).Error().Err(err).Str("port", port).Msg("Failed to parse port number, using default")
			portNum = 14000
		}
		endPoint := &ApiEndPoint{IpAddress: ip, MgmtPort: portNum, lastActive: time.Now()}
		actualEndPoints[e] = endPoint
	}
	a.apiEndpoints.Replace(actualEndPoints)
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
	updateTime := time.Now()

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
					endpoint := &ApiEndPoint{IpAddress: IpAddress, MgmtPort: n.MgmtPort, lastActive: updateTime}
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

	a.apiEndpoints.Replace(newEndpoints)

	// always rotate endpoint to make sure we distribute load between different Weka Nodes
	a.rotateEndpoint(ctx)
	return nil
}

// rotateEndpoint returns a random endpoint of the configured ones
func (a *ApiClient) rotateEndpoint(ctx context.Context) {
	logger := log.Ctx(ctx)
	if a.apiEndpoints.Len() == 0 {
		a.resetDefaultEndpoints(ctx)
	}
	a.apiEndpoints.Rotate()
	current := a.apiEndpoints.Current()
	if current == nil {
		logger.Error().Msg("Failed to choose random endpoint, no endpoints exist")
		return
	}
	logger.Debug().Str("new_endpoint", current.String()).Msg("Switched to new API endpoint")
}

// getEndpoint returns last known endpoint to work against
func (a *ApiClient) getEndpoint(ctx context.Context) *ApiEndPoint {
	if current := a.apiEndpoints.Current(); current != nil {
		return current
	}
	a.resetDefaultEndpoints(ctx)
	return a.apiEndpoints.Current()
}
