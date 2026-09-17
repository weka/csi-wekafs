package wekafs

import "testing"

// Guards the one place the refactor could silently flip meaning: the Suppress* inversion.
func TestDriverConfigOptionsMapping(t *testing.T) {
	dc := NewDriverConfig(DriverConfigOptions{
		SuppressSnapshotSupport:      true,
		SuppressVolumeCloneSupport:   false,
		AdvertiseVolumeHealthSupport: true,
		GrpcRequestTimeoutSeconds:    10,
		MaxCreateVolumeReqs:          3,
		EnforceDirVolTotalCapacity:   true,
	})
	if dc.advertiseSnapshotSupport {
		t.Error("SuppressSnapshotSupport=true must disable snapshot advertisement")
	}
	if !dc.advertiseVolumeCloneSupport {
		t.Error("SuppressVolumeCloneSupport=false must leave clone advertisement on")
	}
	if !dc.advertiseVolumeHealthSupport || !dc.requiresPvCaching() {
		t.Error("volume health support did not carry through")
	}
	if dc.grpcRequestTimeout.Seconds() != 10 {
		t.Errorf("timeout not converted to duration: %v", dc.grpcRequestTimeout)
	}
	if dc.maxConcurrencyPerOp["CreateVolume"] != 3 {
		t.Errorf("concurrency map wrong: %v", dc.maxConcurrencyPerOp)
	}
	// An empty MutuallyExclusiveMountOptions must still get the built-in default set.
	if len(dc.mutuallyExclusiveOptions) != 1 || len(dc.mutuallyExclusiveOptions[0]) != 3 {
		t.Errorf("default mutually exclusive options missing: %v", dc.mutuallyExclusiveOptions)
	}
}
