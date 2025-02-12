package apiclient

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type ApiEndPoint struct {
	IpAddress            string
	MgmtPort             int
	lastActive           time.Time
	failCount            int64
	timeoutCount         int64
	http400ErrCount      int64
	http401ErrCount      int64
	http404ErrCount      int64
	http409ErrCount      int64
	http500ErrCount      int64
	generalErrCount      int64
	transportErrCount    int64
	noRespCount          int64
	parseErrCount        int64
	requestCount         int64
	requestDurationTotal time.Duration
}

func (e *ApiEndPoint) String() string {
	return fmt.Sprintf("%s:%d", e.IpAddress, e.MgmtPort)
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
			log.Ctx(ctx).Error().Str("ip", ip).Msg("Cannot determine a valid hostname, IPv4 or IPv6 address, skipping endpoint")
			continue
		}

		portNum, err := strconv.Atoi(port)
		if err != nil {
			log.Ctx(ctx).Error().Err(err).Str("port", port).Msg("Failed to parse port number, using default")
			portNum = 14000
		}
		endPoint := &ApiEndPoint{
			IpAddress:            ip,
			MgmtPort:             portNum,
			lastActive:           time.Now(),
			failCount:            0,
			requestCount:         0,
			requestDurationTotal: 0,
		}
		actualEndPoints[e] = endPoint
	}
	a.actualApiEndpoints = actualEndPoints
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

	// Create a copy of all existing endpoints to swap without locking
	existingEndpoints := make(map[string]*ApiEndPoint)
	for k, v := range a.actualApiEndpoints {
		newEndPoint := *v
		existingEndpoints[k] = &newEndPoint
	}

	for _, n := range *nodes {
		// Make sure that only backends and not clients are added to the list
		if n.Mode == NodeModeBackend {
			for _, IpAddress := range n.Ips {
				endpointKey := fmt.Sprintf("%s:%d", IpAddress, n.MgmtPort)
				existingEndpoint, ok := existingEndpoints[endpointKey]
				if ok {
					logger.Debug().Str("endpoint", endpointKey).Msg("Updating existing API endpoint")
					existingEndpoint.lastActive = updateTime
					newEndpoints[endpointKey] = existingEndpoint
				} else {
					logger.Info().Str("endpoint", endpointKey).Msg("Adding new API endpoint")
					endpoint := &ApiEndPoint{IpAddress: IpAddress, MgmtPort: n.MgmtPort, lastActive: updateTime, failCount: 0, requestCount: 0, requestDurationTotal: 0}
					newEndpoints[endpointKey] = endpoint
				}
			}
		}
	}
	// prune endpoints which are not active anymore (not existing on cluster)
	for _, endpoint := range existingEndpoints {
		if endpoint.lastActive.Before(updateTime) {
			logger.Warn().Time("endpoint_last_active_time", endpoint.lastActive).Str("endpoint", endpoint.String()).Msg("Removing inactive API endpoint")
			delete(newEndpoints, endpoint.String())
		}
	}

	a.actualApiEndpoints = newEndpoints

	// always rotate endpoint to make sure we distribute load between different Weka Nodes
	a.rotateEndpoint(ctx)
	return nil
}

// rotateEndpoint returns a random endpoint of the configured ones
func (a *ApiClient) rotateEndpoint(ctx context.Context) {
	logger := log.Ctx(ctx)
	if len(a.actualApiEndpoints) == 0 {
		a.resetDefaultEndpoints(ctx)
	}
	if len(a.actualApiEndpoints) == 0 {
		a.currentEndpoint = ""
		logger.Error().Msg("Failed to choose random endpoint, no endpoints exist")
		return
	}
	keys := reflect.ValueOf(a.actualApiEndpoints).MapKeys()
	key := keys[rand.Intn(len(keys))].String()
	logger.Debug().Str("new_endpoint", key).Str("previous_endpoint", a.currentEndpoint).Msg("Switched to new API endpoint")
	a.currentEndpoint = key
}

// getEndpoint returns last known endpoint to work against
func (a *ApiClient) getEndpoint(ctx context.Context) *ApiEndPoint {
	if a.currentEndpoint == "" {
		a.rotateEndpoint(ctx)
	}
	return a.actualApiEndpoints[a.currentEndpoint]
}
