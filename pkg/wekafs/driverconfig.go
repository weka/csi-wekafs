package wekafs

type DriverConfig struct {
	DynamicVolPath                string
	VolumePrefix                  string
	SnapshotPrefix                string
	SeedSnapshotPrefix            string
	allowAutoFsCreation           bool
	allowAutoFsExpansion          bool
	allowAutoSeedSnapshotCreation bool
	allowSnapshotsOfLegacyVolumes bool
	advertiseSnapshotSupport      bool
	advertiseVolumeCloneSupport   bool
	debugPath                     string
}

func NewDriverConfig(dynamicVolPath, VolumePrefix, SnapshotPrefix, SeedSnapshotPrefix, debugPath string,
	allowAutoFsCreation, allowAutoFsExpansion, allowAutoSeedSnapshotCreation, allowSnapshotsOfLegacyVolumes bool,
	suppressnapshotSupport, suppressVolumeCloneSupport bool) *DriverConfig {
	return &DriverConfig{
		DynamicVolPath:                dynamicVolPath,
		VolumePrefix:                  VolumePrefix,
		SnapshotPrefix:                SnapshotPrefix,
		SeedSnapshotPrefix:            SeedSnapshotPrefix,
		allowAutoFsCreation:           allowAutoFsCreation,
		allowAutoFsExpansion:          allowAutoFsExpansion,
		allowAutoSeedSnapshotCreation: allowAutoSeedSnapshotCreation,
		allowSnapshotsOfLegacyVolumes: allowSnapshotsOfLegacyVolumes,
		advertiseSnapshotSupport:      !suppressnapshotSupport,
		advertiseVolumeCloneSupport:   !suppressVolumeCloneSupport,
		debugPath:                     debugPath,
	}
}

func (dc *DriverConfig) isInDebugMode() bool {
	return dc.debugPath != ""
}
