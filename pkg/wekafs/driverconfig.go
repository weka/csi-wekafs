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
	maxRandomWaitIntervalSecs     int
}

func NewDriverConfig(dynamicVolPath, VolumePrefix, SnapshotPrefix, SeedSnapshotPrefix, debugPath string,
	allowAutoFsCreation, allowAutoFsExpansion, allowAutoSeedSnapshotCreation, allowSnapshotsOfLegacyVolumes bool,
	suppressnapshotSupport, suppressVolumeCloneSupport,
	allowInsecureHttps, alwaysAllowSnapshotVolumes bool, maxRandomWaitIntervalSecs int) *DriverConfig {
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
		maxRandomWaitIntervalSecs:     maxRandomWaitIntervalSecs,
	}
}

func (dc *DriverConfig) isInDevMode() bool {
	return dc.debugPath != ""
}
