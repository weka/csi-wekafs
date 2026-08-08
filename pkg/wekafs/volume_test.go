package wekafs

import (
	"context"
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient/apiclienttest"
)

func GetDriverForTest(t *testing.T) *WekaFsDriver {
	nodeId := "localhost"
	mutuallyExclusive := MutuallyExclusiveMountOptsStrings{"readcache,writecache,coherent,forcedirect", "sync,async", "ro,rw"}
	driverConfig := NewDriverConfig(DriverConfigOptions{
		DynamicVolPath:     "csi-volumes",
		VolumePrefix:       "csi-vol-",
		SnapshotPrefix:     "csi-snap-",
		SeedSnapshotPrefix: "csi-seed-snap-",
		Version:            "v1",

		AllowAutoFsCreation:              true,
		AllowAutoFsExpansion:             true,
		AllowSnapshotsOfDirectoryVolumes: true,
		AllowInsecureHttps:               true,
		AlwaysAllowSnapshotVolumes:       true,
		AllowProtocolContainers:          true,
		AllowEncryptionWithoutKms:        true,

		SuppressSnapshotSupport:      true,
		SuppressVolumeCloneSupport:   true,
		AdvertiseVolumeHealthSupport: true,

		MutuallyExclusiveMountOptions: mutuallyExclusive,

		MaxCreateVolumeReqs:        1,
		MaxDeleteVolumeReqs:        1,
		MaxExpandVolumeReqs:        1,
		MaxCreateSnapshotReqs:      1,
		MaxDeleteSnapshotReqs:      1,
		MaxNodePublishVolumeReqs:   1,
		MaxNodeUnpublishVolumeReqs: 1,

		GrpcRequestTimeoutSeconds:     10,
		HealthProbeWekaTimeoutSeconds: 5,

		AllowNfsFailback:   true,
		UseNfs:             true,
		NfsProtocolVersion: "4.1",

		KeepThinProvisioningRatioOnExpand: true,
	})
	driver, err := NewWekaFsDriver("csi.weka.io", nodeId, "unix://tmp/csi.sock", 10, "v1.0", "", CsiModeAll, false, driverConfig)
	if err != nil {
		t.Fatalf("Failed to create new driver: %v", err)
	}
	// driver.Run starts a full gRPC server (and, in controller mode, leader election), all on a
	// background goroutine that races with the caller to set driver.cs. These tests only need a
	// ControllerServer to hand to NewVolumeFromId, so build one directly instead of racing Run's
	// initialization - faster, and it never touches the network or the filesystem.
	driver.cs = NewControllerServer(nodeId, driver.api, nil, driverConfig, nil)
	return driver
}

var creds apiclient.Credentials
var endpoint string
var fsName string
var explicitEndpoint bool

var client *apiclient.ApiClient

// fakeServer is the hermetic, in-memory Weka API used by these tests unless -api-endpoint was
// explicitly passed on the command line (see TestMain). It is nil during a real cluster
// integration run.
var fakeServer *apiclienttest.Server

func TestMain(m *testing.M) {
	flag.StringVar(&endpoint, "api-endpoint", "localhost:14000", "API endpoint for tests")
	flag.StringVar(&creds.Username, "api-username", "admin", "API username for tests")
	flag.StringVar(&creds.Password, "api-password", "", "API password for tests")
	flag.StringVar(&creds.Organization, "api-org", "Root", "API org for tests")
	flag.StringVar(&creds.HttpScheme, "api-scheme", "https", "API scheme for tests")
	flag.StringVar(&fsName, "fs-name", "default", "Filesystem name for tests")
	flag.Parse()

	// See the identical comment in pkg/wekafs/apiclient/apiclient_test.go: by default (no
	// -api-endpoint flag passed) this suite must be hermetic, so it stands up an in-memory fake
	// Weka API server instead of dialing out to a real cluster. Passing -api-endpoint explicitly
	// opts back into a real cluster integration run.
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "api-endpoint" {
			explicitEndpoint = true
		}
	})

	if !explicitEndpoint {
		fakeServer = apiclienttest.NewStandalone()
		endpoint = fakeServer.Addr()
		creds.HttpScheme = "http"
	}

	m.Run()

	if fakeServer != nil {
		fakeServer.Close()
	}
}

func GetApiClientForTest(t *testing.T) *apiclient.ApiClient {
	creds.Endpoints = []string{endpoint}
	if client == nil {
		apiClient, err := apiclient.NewApiClient(context.Background(), creds, apiclient.ApiClientOptions{
			AllowInsecureHttps: true,
			Hostname:           endpoint,
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

func TestScaleThinSsdOnExpand(t *testing.T) {
	cases := []struct {
		name      string
		oldTotal  int64
		oldVal    int64
		newTotal  int64
		keepRatio bool
		want      int64
	}{
		{
			name:     "pinned: val equals total, keepRatio true",
			oldTotal: 100, oldVal: 100, newTotal: 200, keepRatio: true,
			want: 200,
		},
		{
			name:     "val > total scales proportionally",
			oldTotal: 100, oldVal: 120, newTotal: 200, keepRatio: true,
			want: 240, // 120 * 200 / 100
		},
		{
			name:     "overcommit: val < total, keepRatio true, scale",
			oldTotal: 100, oldVal: 50, newTotal: 200, keepRatio: true,
			want: 100, // 50 * 200 / 100
		},
		{
			name:     "overcommit: val < total, keepRatio false, leave unchanged",
			oldTotal: 100, oldVal: 50, newTotal: 200, keepRatio: false,
			want: 50,
		},
		{
			name:     "large scale: no int64 overflow",
			oldTotal: 2 << 50, oldVal: 1 << 50, newTotal: 4 << 50, keepRatio: true,
			want: 2 << 50, // (1<<50) * (4<<50) / (2<<50)
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scaleThinSsdOnExpand(tc.oldTotal, tc.oldVal, tc.newTotal, tc.keepRatio)
			if got != tc.want {
				t.Errorf("scaleThinSsdOnExpand(%d, %d, %d, %v) = %d, want %d",
					tc.oldTotal, tc.oldVal, tc.newTotal, tc.keepRatio, got, tc.want)
			}
		})
	}
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
	testPattern("dir/v1/filesystem/4e1243bd22c66e76c2ba9eddc1f91394e57f9f83/some_dirName", VolumeTypeDirV1)
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
