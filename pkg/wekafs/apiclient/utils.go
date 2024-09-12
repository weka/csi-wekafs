package apiclient

import (
	"fmt"
	"github.com/rs/zerolog/log"
	"hash/fnv"
	"net"
	"os"
	"reflect"
	"strings"
)

// ObjectsAreEqual returns true if both ApiObject have same immutable fields (other fields and nil fields are disregarded)
func ObjectsAreEqual(o1 ApiObject, o2 ApiObject) bool {
	if reflect.TypeOf(o1) != reflect.TypeOf(o2) {
		return false
	}
	ref := reflect.ValueOf(o1)
	oth := reflect.ValueOf(o2)
	for _, field := range o1.getImmutableFields() {
		qval := reflect.Indirect(ref).FieldByName(field)
		val := reflect.Indirect(oth).FieldByName(field)
		if !qval.IsZero() {
			if !reflect.DeepEqual(qval.Interface(), val.Interface()) {
				return false
			}
		}
	}
	return true
}

// ObjectRequestHasRequiredFields returns true if all mandatory fields of object in API request are filled in
func ObjectRequestHasRequiredFields(o ApiObjectRequest) bool {
	ref := reflect.ValueOf(o)
	var missingFields []string
	for _, field := range o.getRequiredFields() {
		if reflect.Indirect(ref).FieldByName(field).IsZero() {
			missingFields = append(missingFields, field)
		}
	}
	if len(missingFields) > 0 {
		log.Error().Strs("missing_fileds", missingFields).Msg("Object is missing mandatory fields")
		return false
	}
	return true
}

// hashString is a simple hash function that takes a string and returns a hash value in the range [0, n)
func hashString(s string, n int) int {
	if n == 0 {
		return 0
	}

	// Create a new FNV-1a hash
	h := fnv.New32a()

	// Write the string to the hash
	_, _ = h.Write([]byte(s))

	// Get the hash sum as a uint32
	hashValue := h.Sum32()

	// Return the hash value in the range of [0, n)
	return int(hashValue % uint32(n))
}

type Network struct {
	IP     net.IP
	Subnet *net.IP
}

func (n *Network) AsNfsRule() string {
	return fmt.Sprintf("%s/%s", n.IP.String(), n.Subnet.String())
}

func parseNetworkString(s string) (*Network, error) {
	var ip, subnet net.IP
	if strings.Contains(s, "/") {
		parts := strings.Split(s, "/")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid CIDR notation: %s", s)
		}
		ip = net.ParseIP(parts[0])
		subnet = net.ParseIP(parts[1])
		if subnet == nil {
			_, ipNet, err := net.ParseCIDR(s)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR notation: %s", s)
			}
			subnet = net.IP(ipNet.Mask)
		}
	} else {
		ip = net.ParseIP(s)
		subnet = net.ParseIP("255.255.255.255")
	}

	return &Network{
		IP:     ip,
		Subnet: &subnet,
	}, nil
}

func (n *Network) ContainsIPAddress(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	_, ipNet, err := net.ParseCIDR(fmt.Sprintf("%s/%s", n.IP.String(), n.Subnet.String()))
	if err != nil {
		return false
	}
	return ipNet.Contains(ip)
}

func GetNodeIpAddress() string {
	ret := os.Getenv("KUBE_NODE_IP_ADDRESS")
	if ret != "" {
		return ret
	}
	return "127.0.0.1"
}
