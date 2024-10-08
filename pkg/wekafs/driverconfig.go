package wekafs

import (
	"github.com/rs/zerolog/log"
	"strings"
	"time"
)

type MutuallyExclusiveMountOptsStrings []string

func (i *MutuallyExclusiveMountOptsStrings) String() string {
	return "Mutually exclusive mount options (those that cannot be set together)"
}
func (i *MutuallyExclusiveMountOptsStrings) Set(value string) error {
	*i = append(*i, value)
	return nil
}

type DriverConfig struct {
	DynamicVolPath                string
	VolumePrefix                  string
	SnapshotPrefix                string
	SeedSnapshotPrefix            string
	allowAutoFsCreation           bool
	allowAutoFsExpansion          bool
	allowSnapshotsOfLegacyVolumes bool
	advertiseSnapshotSupport      bool
	advertiseVolumeCloneSupport   bool
	debugPath                     string
	allowInsecureHttps            bool
	alwaysAllowSnapshotVolumes    bool
	mutuallyExclusiveOptions      []mutuallyExclusiveMountOptionSet
	maxConcurrencyPerOp           map[string]int64
	grpcRequestTimeout            time.Duration
	allowProtocolContainers       bool
	allowNfsFailback              bool
	useNfs                        bool
	interfaceGroupName            string
	clientGroupName               string
	nfsProtocolVersion            string
}

func (dc *DriverConfig) Log() {
	log.Info().Str("dynamic_vol_path", dc.DynamicVolPath).
		Str("volume_prefix", dc.VolumePrefix).Str("snapshot_prefix", dc.SnapshotPrefix).Str("seed_snapshot_prefix", dc.SnapshotPrefix).
		Bool("allow_auto_fs_creation", dc.allowAutoFsCreation).Bool("allow_auto_fs_expansion", dc.allowAutoFsExpansion).
		Bool("advertise_snapshot_support", dc.advertiseSnapshotSupport).Bool("advertise_volume_clone_support", dc.advertiseVolumeCloneSupport).
		Bool("allow_insecure_https", dc.allowInsecureHttps).Bool("always_allow_snapshot_volumes", dc.alwaysAllowSnapshotVolumes).
		Interface("mutually_exclusive_mount_options", dc.mutuallyExclusiveOptions).
		Int64("max_create_volume_reqs", dc.maxConcurrencyPerOp["CreateVolume"]).
		Int64("max_delete_volume_reqs", dc.maxConcurrencyPerOp["DeleteVolume"]).
		Int64("max_expand_volume_reqs", dc.maxConcurrencyPerOp["ExpandVolume"]).
		Int64("max_create_snapshot_reqs", dc.maxConcurrencyPerOp["CreateSnapshot"]).
		Int64("max_delete_snapshot_reqs", dc.maxConcurrencyPerOp["DeleteSnapshot"]).
		Int64("max_node_publish_volume_reqs", dc.maxConcurrencyPerOp["NodePublishVolume"]).
		Int64("max_node_unpublish_volume_reqs", dc.maxConcurrencyPerOp["NodeUnpublishVolume"]).
		Int("grpc_request_timeout_seconds", int(dc.grpcRequestTimeout.Seconds())).
		Bool("allow_protocol_containers", dc.allowProtocolContainers).
		Bool("allow_nfs_failback", dc.allowNfsFailback).
		Bool("use_nfs", dc.useNfs).
		Str("interface_group_name", dc.interfaceGroupName).
		Str("client_group_name", dc.clientGroupName).
		Msg("Starting driver with the following configuration")

}
func NewDriverConfig(dynamicVolPath, VolumePrefix, SnapshotPrefix, SeedSnapshotPrefix, debugPath string,
	allowAutoFsCreation, allowAutoFsExpansion, allowSnapshotsOfLegacyVolumes bool,
	suppressnapshotSupport, suppressVolumeCloneSupport, allowInsecureHttps, alwaysAllowSnapshotVolumes bool,
	mutuallyExclusiveMountOptions MutuallyExclusiveMountOptsStrings,
	maxCreateVolumeReqs, maxDeleteVolumeReqs, maxExpandVolumeReqs, maxCreateSnapshotReqs, maxDeleteSnapshotReqs, maxNodePublishVolumeReqs, maxNodeUnpublishVolumeReqs int64,
	grpcRequestTimeoutSeconds int,
	allowProtocolContainers bool,
	allowNfsFailback, useNfs bool,
	interfaceGroupName, clientGroupName, nfsProtocolVersion string,
) *DriverConfig {

	var MutuallyExclusiveMountOptions []mutuallyExclusiveMountOptionSet
	for _, exclusiveSet := range mutuallyExclusiveMountOptions {
		opts := strings.Split(exclusiveSet, ",")
		MutuallyExclusiveMountOptions = append(MutuallyExclusiveMountOptions, opts)
	}
	if len(MutuallyExclusiveMountOptions) == 0 {
		MutuallyExclusiveMountOptions = append(MutuallyExclusiveMountOptions, []string{MountOptionWriteCache, MountOptionCoherent, MountOptionReadCache})
	}

	grpcRequestTimeout := time.Duration(grpcRequestTimeoutSeconds) * time.Second

	concurrency := make(map[string]int64)
	concurrency["CreateVolume"] = maxCreateVolumeReqs
	concurrency["DeleteVolume"] = maxDeleteVolumeReqs
	concurrency["ExpandVolume"] = maxExpandVolumeReqs
	concurrency["CreateSnapshot"] = maxCreateSnapshotReqs
	concurrency["DeleteSnapshot"] = maxDeleteSnapshotReqs
	concurrency["NodePublishVolume"] = maxNodePublishVolumeReqs
	concurrency["NodeUnpublishVolume"] = maxNodeUnpublishVolumeReqs

	return &DriverConfig{
		DynamicVolPath:                dynamicVolPath,
		VolumePrefix:                  VolumePrefix,
		SnapshotPrefix:                SnapshotPrefix,
		SeedSnapshotPrefix:            SeedSnapshotPrefix,
		allowAutoFsCreation:           allowAutoFsCreation,
		allowAutoFsExpansion:          allowAutoFsExpansion,
		allowSnapshotsOfLegacyVolumes: allowSnapshotsOfLegacyVolumes,
		advertiseSnapshotSupport:      !suppressnapshotSupport,
		advertiseVolumeCloneSupport:   !suppressVolumeCloneSupport,
		debugPath:                     debugPath,
		allowInsecureHttps:            allowInsecureHttps,
		alwaysAllowSnapshotVolumes:    alwaysAllowSnapshotVolumes,
		mutuallyExclusiveOptions:      MutuallyExclusiveMountOptions,
		maxConcurrencyPerOp:           concurrency,
		grpcRequestTimeout:            grpcRequestTimeout,
		allowProtocolContainers:       allowProtocolContainers,
		allowNfsFailback:              allowNfsFailback,
		useNfs:                        useNfs,
		interfaceGroupName:            interfaceGroupName,
		clientGroupName:               clientGroupName,
		nfsProtocolVersion:            nfsProtocolVersion,
	}
}

func (dc *DriverConfig) isInDevMode() bool {
	return dc.debugPath != ""
}
