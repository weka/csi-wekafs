package apiclient

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetNfsInterfaceGroup(t *testing.T) {
	apiClient := GetApiClientForTest(t)
	// Mock GetInterfaceGroupsByType method

	// Test case: Valid interface group
	ig := apiClient.GetNfsInterfaceGroup(context.Background(), "")
	assert.NotNil(t, ig)
	assert.Contains(t, apiClient.NfsInterfaceGroups, "default")

	// Test case: Invalid interface group
	ig = apiClient.GetNfsInterfaceGroup(context.Background(), "NFS")
	assert.Nil(t, ig)

	// Test case: Invalid interface group
	ig = apiClient.GetNfsInterfaceGroup(context.Background(), "Data")
	assert.NotNil(t, ig)
	assert.Contains(t, apiClient.NfsInterfaceGroups, "Data")

}

func TestGetNfsMountIp(t *testing.T) {
	apiClient := GetApiClientForTest(t)
	// Mock GetInterfaceGroupsByType method

	// Test case: Valid interface group
	ip, err := apiClient.GetNfsMountIp(context.Background(), "")
	assert.NoError(t, err)
	assert.NotEmpty(t, ip)

	// Test case: Valid interface group
	ip, err = apiClient.GetNfsMountIp(context.Background(), "NFS")
	assert.NoError(t, err)
	assert.NotEmpty(t, ip)

}
