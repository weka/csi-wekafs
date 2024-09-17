package apiclient

import (
	"github.com/rs/zerolog/log"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashString(t *testing.T) {
	testCases := []struct {
		input    string
		n        int
		expected int
	}{
		{"test", 10, 5},
		{"example", 10, 9},
		{"hash", 10, 1},
		{"string", 10, 8},
		{"", 10, 1},
		{"osi415-zbjgk-worker-0-t6g55", 10, 5},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := hashString(tc.input, tc.n)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestGetNodeIpAddressByRouting(t *testing.T) {
	testCases := []struct {
		targetHost string
	}{
		{"8.8.8.8"},
		{"1.1.1.1"},
		{"localhost"},
	}

	for _, tc := range testCases {
		t.Run(tc.targetHost, func(t *testing.T) {
			ip, err := GetNodeIpAddressByRouting(tc.targetHost)
			assert.NoError(t, err)
			assert.NotEmpty(t, ip)
			log.Info().Str("ip", ip).Msg("Node IP address")
		})
	}
}
