package wekafs

import (
	"fmt"
	"net"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func validateEndpoint(endpoint string) error {
	if strings.Contains(endpoint, "://") {
		return status.Errorf(codes.InvalidArgument, "endpoint %s should not include protocol prefix", endpoint)
	}

	// Parse IP address and port
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("endpoint must include a port (e.g., 192.168.1.1:14000): %s", endpoint)
	}

	// Validate IP address
	if net.ParseIP(host) == nil {
		return fmt.Errorf("invalid IP address: %s", host)
	}

	return nil
}

func getEndpointsFromRaw(endpointsRaw string) ([]string, error) {
	var ret []string
	for _, s := range strings.Split(endpointsRaw, ",") {
		endpoint := trimValue(s)
		err := validateEndpoint(endpoint)
		if err != nil {
			return nil, err
		}
		ret = append(ret, endpoint)
	}
	if len(ret) == 0 {
		return nil, fmt.Errorf("no valid endpoints found")
	}
	return ret, nil
}
