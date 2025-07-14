package apiclient

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"maps"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ApiEndPoint struct {
	IpAddress  string
	MgmtPort   int
	lastActive time.Time
	raftRole   string // used to store raft role of the node, if any
}

func (e *ApiEndPoint) String() string {
	return fmt.Sprintf("%s:%d", e.IpAddress, e.MgmtPort)
}

func (a *ApiClient) resetDefaultEndpoints(ctx context.Context) {
	a.apiEndpoints.UpdateEndpointsFromAddresses(ctx, a.Credentials.Endpoints)
}

// fetchMountEndpoints used to obtain actual data plane IP addresses
func (a *ApiClient) fetchMountEndpoints(ctx context.Context) error {
	log.Ctx(ctx).Trace().Msg("Fetching mount endpoints")
	a.MountEndpoints = []string{}
	nodes := &WekaNodes{}
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

// UpdateApiEndpointsFromCluster fetches current management IP addresses of the cluster
func (a *ApiClient) UpdateApiEndpointsFromCluster(ctx context.Context) error {
	logger := log.Ctx(ctx)
	nodes := &WekaNodes{}
	err := a.GetNodesByRole(ctx, NodeRoleManagement, nodes)
	if err != nil {
		return err
	}
	if len(*nodes) == 0 {
		logger.Error().Msg("No management nodes found, not updating endpoints")
		return errors.New("no management nodes found, could not update api endpoints")
	}

	// Create a copy of all existing endpoints to swap without locking
	addresses := []string{}

	for _, n := range *nodes {
		// Make sure that only backends and not clients are added to the list
		if n.Mode == NodeModeBackend {
			for _, IpAddress := range n.Ips {
				endpointKey := fmt.Sprintf("%s:%d", IpAddress, n.MgmtPort)
				addresses = append(addresses, endpointKey)
			}
		}
	}
	a.apiEndpoints.UpdateEndpointsFromAddresses(ctx, addresses)
	// always rotate endpoint to make sure we distribute load between different Weka Nodes
	a.rotateEndpoint(ctx)
	return nil
}

// rotateEndpoint returns a random endpoint of the configured ones
func (a *ApiClient) rotateEndpoint(ctx context.Context) {
	logger := log.Ctx(ctx)
	p := a.getEndpoint(ctx).String()
	a.apiEndpoints.RotateEndpoint()
	if !a.RotateEndpointOnEachRequest {
		n := a.getEndpoint(ctx).String()
		logger.Debug().Str("new_endpoint", n).Str("previous_endpoint", p).Msg("Switched to new API endpoint")
	}
}

// getEndpoint returns last known endpoint to work against
func (a *ApiClient) getEndpoint(ctx context.Context) *ApiEndPoint {
	return a.apiEndpoints.GetEndpoint()
}

type ApiEndPoints struct {
	sync.RWMutex
	currentEndpoint *ApiEndPoint
	endpoints       map[string]*ApiEndPoint
}

func NewApiEndPoints() *ApiEndPoints {
	return &ApiEndPoints{
		endpoints: make(map[string]*ApiEndPoint),
	}
}

func (eps *ApiEndPoints) AddOrUpdateEndpoint(ctx context.Context, endpoint *ApiEndPoint) {
	logger := log.Ctx(ctx)
	if endpoint == nil {
		return
	}
	key := endpoint.String()
	eps.Lock()
	defer eps.Unlock()
	if existing, ok := eps.endpoints[key]; ok {
		existing.IpAddress = endpoint.IpAddress // update IP address
		existing.MgmtPort = endpoint.MgmtPort   // update management port
		existing.lastActive = time.Now()        // update last active time
		existing.raftRole = endpoint.raftRole   // update raft role if provided
		logger.Debug().Str("endpoint", key).Msg("Updating existing API endpoint")
	} else {
		eps.endpoints[key] = endpoint // add new endpoint
	}
	if endpoint.lastActive.IsZero() {
		endpoint.lastActive = time.Now() // set last active time if not set
	}
}

func (eps *ApiEndPoints) UpdateEndpointsFromAddresses(ctx context.Context, addresses []string) {
	// populate default endpoints from credentials
	endpointAddresses := make(map[string]struct{})
	for _, e := range addresses {
		endpointAddresses[e] = struct{}{}
	}

	//
	for e := range endpointAddresses {
		endPoint := constructEndpointFromAddress(ctx, e)
		eps.AddOrUpdateEndpoint(ctx, endPoint)
	}

	// remove endpoints that are not in the current list of endpoints
	currentEps := eps.GetEndpoints()
	for key := range maps.Keys(currentEps) {
		if _, ok := endpointAddresses[key]; !ok {
			log.Ctx(ctx).Debug().Str("endpoint", key).Msg("Removing endpoint not in the current list of endpoints")
			eps.RemoveEndpoint(key)
		}
	}
}

func (eps *ApiEndPoints) GetEndpoint() *ApiEndPoint {
	var cur *ApiEndPoint

	for cur == nil {
		eps.RLock()
		cur = eps.currentEndpoint
		eps.RUnlock()
		if cur == nil {
			eps.RotateEndpoint()
		}
	}

	return cur
}

func (eps *ApiEndPoints) GetEndpoints() map[string]*ApiEndPoint {
	eps.RLock()
	defer eps.RUnlock()
	return eps.endpoints
}

func (eps *ApiEndPoints) GetEndpointByRaftRole(role string) *ApiEndPoint {
	eps.RLock()
	defer eps.RUnlock()
	for _, endpoint := range eps.endpoints {
		if endpoint.raftRole == role {
			return endpoint
		}
	}
	return nil // no endpoint found with the specified raft role
}

func (eps *ApiEndPoints) RotateEndpoint() {
	eps.Lock()
	defer eps.Unlock()
	if len(eps.endpoints) == 0 {
		return // nothing to rotate
	}
	keys := reflect.ValueOf(eps.endpoints).MapKeys()
	key := keys[rand.Intn(len(keys))].String()
	eps.currentEndpoint = eps.endpoints[key]
}

func (eps *ApiEndPoints) RemoveEndpoint(endpoint string) {
	eps.Lock()
	defer eps.Unlock()
	if endpoint == "" {
		return
	}
	key := endpoint
	if _, ok := eps.endpoints[key]; ok {
		delete(eps.endpoints, key)
		if eps.currentEndpoint != nil && eps.currentEndpoint.String() == key {
			eps.currentEndpoint = nil // reset current endpoint if it was removed
		}
	}
}

func constructEndpointFromAddress(ctx context.Context, e string) *ApiEndPoint {
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
		return nil
	}

	portNum, err := strconv.Atoi(port)
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Str("port", port).Msg("Failed to parse port number, using default")
		portNum = 14000
	}
	endPoint := &ApiEndPoint{
		IpAddress:  ip,
		MgmtPort:   portNum,
		lastActive: time.Now(),
	}
	return endPoint
}
