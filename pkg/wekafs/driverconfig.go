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
	allowInsecureHttps            bool
	alwaysAllowSnapshotVolumes    bool
}

func NewDriverConfig(dynamicVolPath, VolumePrefix, SnapshotPrefix, SeedSnapshotPrefix, debugPath string,
	allowAutoFsCreation, allowAutoFsExpansion, allowAutoSeedSnapshotCreation, allowSnapshotsOfLegacyVolumes bool,
	suppressnapshotSupport, suppressVolumeCloneSupport,
	allowInsecureHttps, alwaysAllowSnapshotVolumes bool) *DriverConfig {
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
		allowInsecureHttps:            allowInsecureHttps,
		alwaysAllowSnapshotVolumes:    alwaysAllowSnapshotVolumes,
	}
}

func (dc *DriverConfig) isInDebugMode() bool {
	return dc.debugPath != ""
}
