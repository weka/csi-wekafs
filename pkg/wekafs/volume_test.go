package wekafs

import (
	"context"
	"flag"
	"github.com/stretchr/testify/assert"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"testing"
	"time"
)

func GetDriverForTest(t *testing.T) *WekaFsDriver {
	ctx := context.Background()
	nodeId := "localhost"
	mutuallyExclusive := MutuallyExclusiveMountOptsStrings{"readcache,writecache,coherent,forcedirect", "sync,async", "ro,rw"}
	driverConfig := NewDriverConfig(
		"csi-volumes",
		"csi-vol-", "csi-snap-",
		"csi-seed-snap-",
		true,
		true,
		true,
		true,
		true,
		true,
		true,
		mutuallyExclusive,
		1,
		1,
		1,
		1,
		1,
		1,
		1,
		10,
		1,
		true,
		true,
		true,
		"",
		"",
		"4.1",
		"v1",
		false,
		false,
		true,
		"",
		false,
		60*time.Second,
		5,
		false,
		10,
		60*time.Second,
		120*time.Second,
		false,
	)
	driver, err := NewWekaFsDriver("csi.weka.io", nodeId, "unix://tmp/csi.sock", 10, "v1.0", CsiModeAll, false, driverConfig)
	if err != nil {
		t.Fatalf("Failed to create new driver: %v", err)
	}
	go driver.Run(ctx)
	return driver
}

var creds apiclient.Credentials
var endpoint string
var fsName string

var globalClient *apiclient.ApiClient

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
	if globalClient == nil {
		apiClient, err := apiclient.NewApiClient(context.Background(), creds, apiclient.ApiClientOptions{
			AllowInsecureHttps: true,
			Hostname:           endpoint,
			DriverName:         "csi.weka.io",
			ApiTimeout:         apiclient.ApiHttpTimeOutSeconds,
		})
		if err != nil {
			t.Fatalf("Failed to create API client: %v", err)
		}
		if apiClient == nil {
			t.Fatalf("Failed to create API client")
		}
		if err := apiClient.Login(context.Background()); err != nil {
			t.Fatalf("Failed to login: %v", err)
		}
		globalClient = apiClient
	}
	return globalClient
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

func TestVolumeType(t *testing.T) {
	testPattern := func(s string, vt VolumeType) {
		str := sliceVolumeTypeFromVolumeId(s)
		if str != vt {
			t.Errorf("VolumeID: %s, FAILED: %s", s, str)
			return
		}
		t.Logf("PASS: VolumeId:%s (%s)", s, vt)
	}

	testPattern("dir/v1/filesystem/4e1243bd22c66e76c2ba9eddc1f91394e57f9f83-some_dirName", VolumeTypeDirV1)
	testPattern("weka/v1/filesystem", VolumeTypeUnified)
	testPattern("weka/v1/filesystem:snapshotname", VolumeTypeUnified)
	testPattern("weka/v1/filesystem:snapshotname/dirascii-some_dirName", VolumeTypeUnified)
	testPattern("weka/v1/filesystem/dirascii-some_dirName", VolumeTypeUnified)
}

func TestVolumeId(t *testing.T) {
	testPattern := func(s string) {
		err := validateVolumeId(s)
		if err != nil {
			t.Errorf("VolumeID: %s, FAILED: %s", s, err)
			return
		}
		t.Logf("PASS: VolumeId:%s (%s)", s, sliceVolumeTypeFromVolumeId(s))
	}

	testBadPattern := func(s string) {
		err := validateVolumeId(s)
		if err == nil {
			t.Errorf("VolumeID: %s (%s), FALSE PASS", s, sliceVolumeTypeFromVolumeId(s))
			return
		}
		t.Logf("PASS: VolumeId:%s, did not validate, err: %s", s, err)
	}
	// DirVolume
	testPattern("dir/v1/filesystem/4e1243bd22c66e76c2ba9eddc1f91394e57f9f83-some_dirName")
	testBadPattern("dir/v1/filesystem") // only filesystem name, no internal path
	testBadPattern("dir/v1")            // no filesystem name, only volumeType
	testBadPattern("/var/log/messages") // volumeType starts with / - bad path

	// FsVolume
	testPattern("fs/v1/filesystem") // OK
	testBadPattern("fs/v1")         // no filesystem name, only volumeType
	testBadPattern("fs/filesystem") // no version

	// FsSnap
	testBadPattern("fssnap/v1") // no filesystem and no snapshot

	testPattern("weka/v1/filesystem")
	testPattern("weka/v1/filesystem:snapshotname")
	testPattern("weka/v1/filesystem:snapshotname/dirascii-some_dirName")
	testPattern("weka/v1/filesystem/dirascii-some_dirName")
}
