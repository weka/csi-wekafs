package apiclient

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/showa-93/go-mask"
	"hash/fnv"
	"net"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"
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

func (n *Network) GetMaskBits() int {
	ip := n.Subnet.To4()
	if ip == nil {
		return 0
	}
	// Count the number of 1 bits
	mask := binary.BigEndian.Uint32(ip)

	// Count the number of set bits
	cidrBits := 0
	for mask != 0 {
		cidrBits += int(mask & 1)
		mask >>= 1
	}

	return cidrBits
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
	if err != nil || ipNet == nil {
		_, ipNet, err = net.ParseCIDR(fmt.Sprintf("%s/%d", n.IP.String(), n.GetMaskBits()))
		if err != nil || ipNet == nil {
			return false
		}
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

func GetNodeIpAddressByRouting(targetHost string) (string, error) {
	rAddr, err := net.ResolveUDPAddr("udp", targetHost+":80")
	if err != nil {
		return "", err
	}

	// Create a UDP connection to the resolved IP address
	conn, err := net.DialUDP("udp", nil, rAddr)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()

	// Set a deadline for the connection
	err = conn.SetDeadline(time.Now().Add(1 * time.Second))
	if err != nil {
		return "", err
	}

	// Get the local address from the UDP connection
	localAddr := conn.LocalAddr()
	if localAddr == nil {
		return "", errors.New("failed to get local address")
	}

	// Extract the IP address from the local address
	localIP, _, err := net.SplitHostPort(localAddr.String())
	if err != nil {
		return "", err
	}

	return localIP, nil
}

func isValidIPv6Address(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	return ip != nil && ip.To4() == nil && ip.To16() != nil
}

func isValidIPv4Address(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	return ip != nil && ip.To4() != nil
}

func isValidHostname(hostname string) bool {
	if len(hostname) > 253 {
		return false
	}

	// Regex to match the general structure of a hostname.
	// Each label must start and end with an alphanumeric character,
	// may contain hyphens, and be 1 to 63 characters long.
	hostnameRegex := regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9][a-zA-Z0-9-]{0,61}[a-zA-Z0-9]$`)

	return hostnameRegex.MatchString(hostname)
}

// marshalRequest converts interface to bytes
func marshalRequest(r interface{}) (*[]byte, error) {
	j, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func maskPayload(payload string) string {
	masker := mask.NewMasker()
	masker.RegisterMaskStringFunc(mask.MaskTypeFilled, masker.MaskFilledString)
	masker.RegisterMaskField("username", "filled4")
	masker.RegisterMaskField("password", "filled4")
	masker.RegisterMaskField("access_token", "filled4")
	masker.RegisterMaskField("mount_token", "filled4")
	masker.RegisterMaskField("refresh_token", "filled4")
	masker.RegisterMaskField("kms_vault_role_id", "filled4")
	masker.RegisterMaskField("kms_vault_secret_id", "filled4")
	var target any
	err := json.Unmarshal([]byte(payload), &target)
	if err != nil {
		return payload
	}
	masked, _ := masker.Mask(target)
	ret, _ := json.Marshal(masked)
	return string(ret)
}

func generalizeUrlPathForMetrics(urlPath string) string {
	// Replace any numeric IDs in the URL path with a placeholder
	re := regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`)
	// Replace GUIDs with a placeholder
	path := re.ReplaceAllString(urlPath, "{guid}")
	re = regexp.MustCompile(`\b\d+\b`)
	path = re.ReplaceAllString(path, "{id}")
	if strings.HasSuffix(path, "/") {
		return path[:len(path)-1]
	}
	return path
}
