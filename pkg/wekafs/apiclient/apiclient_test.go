package apiclient

import (
	"context"
	"flag"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestGenerateHash(t *testing.T) {
	credentials := Credentials{
		Username:  "testuser",
		Password:  "testpassword",
		Endpoints: []string{"127.0.0.1:14000"},
	}
	apiClient := &ApiClient{
		Credentials: credentials,
	}

	hash := apiClient.generateHash()
	assert.NotZero(t, hash, "Expected non-zero hash value")

	// Test that the hash is consistent for the same credentials
	hash2 := apiClient.generateHash()
	assert.Equal(t, hash, hash2, "Expected hash values to be equal for the same credentials")

	// Test that the hash changes for different credentials
	apiClient.Credentials.Username = "differentuser"
	hash3 := apiClient.generateHash()
	assert.NotEqual(t, hash, hash3, "Expected hash values to be different for different credentials")
}

func TestMaskPayload(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		expected string
	}{
		{
			name: "Mask username and password",
			payload: `{
				"username": "user123",
				"password": "pass123"
			}`,
			expected: `{
				"username": "****",
				"password": "****"
			}`,
		},
		{
			name: "Mask access_token and refresh_token",
			payload: `{
				"access_token": "token123",
				"refresh_token": "refresh123"
			}`,
			expected: `{
				"access_token": "****",
				"refresh_token": "****"
			}`,
		},
		{
			name: "No sensitive fields",
			payload: `{
				"data": "value"
			}`,
			expected: `{
				"data": "value"
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			masked := maskPayload(tt.payload)
			assert.JSONEq(t, tt.expected, masked)
		})
	}
}

func TestIsValidIPv6Address(t *testing.T) {
	tests := []struct {
		ip       string
		expected bool
	}{
		{"2001:0db8:85a3:0000:0000:8a2e:0370:7334", true},
		{"::1", true},
		{"::", true},
		{"2001:db8::ff00:42:8329", true},
		{"1200::AB00:1234::2552:7777:1313", false},
		{"1200::AB00:1234:O000:2552:7777:1313", false},
		{"192.168.1.1", false},
		{"invalid_ip", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			assert.Equal(t, tt.expected, isValidIPv6Address(tt.ip))
		})
	}
}

func TestIsValidIPv4Address(t *testing.T) {
	tests := []struct {
		ip       string
		expected bool
	}{
		{"192.168.1.1", true},
		{"255.255.255.255", true},
		{"0.0.0.0", true},
		{"127.0.0.1", true},
		{"256.256.256.256", false},
		{"192.168.1.256", false},
		{"::1", false},
		{"invalid_ip", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			assert.Equal(t, tt.expected, isValidIPv4Address(tt.ip))
		})
	}
}

func TestIsValidHostname(t *testing.T) {
	tests := []struct {
		hostname string
		expected bool
	}{
		{"example.com", true},
		{"sub.example.com", true},
		{"localhost", true},
		{"a.com", true},
		{"a.b.c.d.e.f.g.h.i.j.k.l.m.n.o.p.q.r.s.t.u.v.w.x.y.z.com", true},
		{"-example.com", false},
		{"example-.com", false},
		{"example..com", false},
		{"example.com-", false},
		{"example.com.", false},
		{"", false},
		{"a..com", false},
		{"a_b.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			assert.Equal(t, tt.expected, isValidHostname(tt.hostname))
		})
	}
}

var creds Credentials
var endpoint string
var fsName string

var client *ApiClient

func TestMain(m *testing.M) {
	flag.StringVar(&endpoint, "api-endpoint", "localhost:14000", "API endpoint for tests")
	flag.StringVar(&creds.Username, "api-username", "admin", "API username for tests")
	flag.StringVar(&creds.Password, "api-password", "", "API password for tests")
	flag.StringVar(&creds.Organization, "api-org", "Root", "API org for tests")
	flag.StringVar(&creds.HttpScheme, "api-scheme", "https", "API scheme for tests")
	flag.StringVar(&fsName, "fs-name", "default", "Filesystem name for tests")
	flag.Parse()
	m.Run()
}

func GetApiClientForTest(t *testing.T) *ApiClient {
	creds.Endpoints = []string{endpoint}
	if client == nil {
		apiClient, err := NewApiClient(context.Background(), creds, true, endpoint)
		if err != nil {
			t.Fatalf("Failed to create API client: %v", err)
		}
		if apiClient == nil {
			t.Fatalf("Failed to create API client")
		}
		if err := apiClient.Login(context.Background()); err != nil {
			t.Fatalf("Failed to login: %v", err)
		}
		client = apiClient
	}
	return client
}
