package wekafs

import (
	"context"
	"flag"
	"github.com/stretchr/testify/assert"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"testing"
)

func GetDriverForTest(t *testing.T) *WekaFsDriver {
	ctx := context.Background()
	nodeId := "localhost"
	mutuallyExclusive := MutuallyExclusiveMountOptsStrings{"readcache,writecache,coherent,forcedirect", "sync,async", "ro,rw"}
	driverConfig := NewDriverConfig("csi-volumes", "csi-vol-", "csi-snap-", "csi-seed-snap-",
		"", true, true, true, true, true,
		true, true, mutuallyExclusive,
		1, 1, 1, 1, 1, 1, 1, 10,
		true, true, true, "", "", "4.1", "v1", false, false, true, "")
	driver, err := NewWekaFsDriver("csi.weka.io", nodeId, "unix://tmp/csi.sock", 10, "v1.0", "", CsiModeAll, false, driverConfig)
	if err != nil {
		t.Fatalf("Failed to create new driver: %v", err)
	}
	go driver.Run(ctx)
	return driver
}

var creds apiclient.Credentials
var endpoint string
var fsName string

var client *apiclient.ApiClient

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

func GetApiClientForTest(t *testing.T) *apiclient.ApiClient {
	creds.Endpoints = []string{endpoint}
	if client == nil {
		apiClient, err := apiclient.NewApiClient(context.Background(), creds, true, endpoint)
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

func TestVolume_getFilesystemFreeSpaceByApi(t *testing.T) {
	driver := GetDriverForTest(t)
	apiClient := GetApiClientForTest(t)
	ctx := context.Background()
	volume, err := NewVolumeFromId(ctx, "weka/v2/default", apiClient, driver.cs)
	if err != nil {
		t.Fatalf("Failed to create volume: %v", err)
	}
	free, err := volume.getFilesystemFreeSpaceByApi(ctx)
	assert.NoError(t, err)
	assert.NotZero(t, free)

}

func TestVolume_getFilesystemFreeSpace(t *testing.T) {
	driver := GetDriverForTest(t)
	apiClient := GetApiClientForTest(t)
	ctx := context.Background()
	volume, err := NewVolumeFromId(ctx, "weka/v2/default", apiClient, driver.cs)
	if err != nil {
		t.Fatalf("Failed to create volume: %v", err)
	}
	free, err := volume.getFilesystemFreeSpace(ctx)
	assert.NoError(t, err)
	assert.NotZero(t, free)
}
