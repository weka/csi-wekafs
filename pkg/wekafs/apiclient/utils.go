package apiclient

import (
	"fmt"
	"github.com/rs/zerolog/log"
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
	const prime = 31
	hash := 0
	for _, char := range s {
		hash = hash*prime + int(char)
	}
	return hash % n
}

type Network struct {
	IP     net.IP
	Subnet *net.IP
	IsCIDR bool
}

func parseNetworkString(s string) (*Network, error) {
	var ip, subnet net.IP
	var isCIDR bool

	if strings.Contains(s, "/") {
		parts := strings.Split(s, "/")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid CIDR notation: %s", s)
		}
		ip = net.ParseIP(parts[0])
		subnet = net.ParseIP(parts[1])
		isCIDR = true
	} else {
		ip = net.ParseIP(s)
		subnet = nil
		isCIDR = false
	}

	if ip == nil {
		return nil, fmt.Errorf("invalid IP address: %s", s)
	}

	return &Network{
		IP:     ip,
		Subnet: &subnet,
		IsCIDR: isCIDR,
	}, nil
}

func (n *Network) ContainsIPAddress(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	if n.IsCIDR {
		_, ipNet, err := net.ParseCIDR(fmt.Sprintf("%s/%s", n.IP.String(), n.Subnet.String()))
		if err != nil {
			return false
		}
		return ipNet.Contains(ip)
	}

	return n.IP.Equal(ip)
}

func GetNodeIpAddress() string {
	ret := os.Getenv("KUBE_NODE_IP_ADDRESS")
	if ret != "" {
		return ret
	}
	return "127.0.0.1"
}
