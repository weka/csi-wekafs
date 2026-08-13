package wekafs

import (
	"strings"
	"time"

	"github.com/rs/zerolog/log"
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
	DynamicVolPath                    string
	VolumePrefix                      string
	SnapshotPrefix                    string
	SeedSnapshotPrefix                string
	allowAutoFsCreation               bool
	allowAutoFsExpansion              bool
	allowSnapshotsOfDirectoryVolumes  bool
	advertiseSnapshotSupport          bool
	advertiseVolumeCloneSupport       bool
	advertiseVolumeHealthSupport      bool
	debugPath                         string
	allowInsecureHttps                bool
	alwaysAllowSnapshotVolumes        bool
	mutuallyExclusiveOptions          []mutuallyExclusiveMountOptionSet
	maxConcurrencyPerOp               map[string]int64
	wekaApiTimeout                    time.Duration // bounds a single Weka API request
	grpcRequestTimeout                time.Duration
	healthProbeWekaTimeout            time.Duration
	allowProtocolContainers           bool
	allowNfsFailback                  bool
	useNfs                            bool
	interfaceGroupName                string
	clientGroupName                   string
	nfsProtocolVersion                string
	csiVersion                        string
	skipGarbageCollection             bool
	waitForObjectDeletion             bool
	allowEncryptionWithoutKms         bool
	driverRef                         *WekaFsDriver
	tracingUrl                        string
	manageNodeTopologyLabels          bool
	wekafsContainerName               string
	enforceDirVolTotalCapacity        bool
	setOwnershipOnDynamicFilesystems  bool
	allowMountOptionOverrides         bool
	keepThinProvisioningRatioOnExpand bool

	// Metrics server settings. Whether a MetricsServer is constructed at all in NewWekaFsDriver is
	// gated on csiMode (CsiModeMetricsServer or CsiModeAll), not on any of these; the settings below
	// only matter to the metrics server lifecycle (see metricsserver.go) once it is running.
	metricsFetchInterval              time.Duration // how often the PV streamer refreshes its list and re-polls Weka for stats
	metricsFetchConcurrentRequests    int           // concurrency cap for both PV processing and single-metric fetches
	enableMetricsServerLeaderElection bool          // gate the metrics server's readiness on holding leadership
	quotaFetchConcurrentRequests      int           // concurrency cap for refreshing per-filesystem quota maps
	quotaCacheValidityDuration        time.Duration // how long a fetched quota map (or single quota) is considered fresh
	useQuotaMapsForMetrics            bool          // fetch quotas in filesystem-wide batches instead of one request per volume
}

func (dc *DriverConfig) Log() {
	log.Info().Str("dynamic_vol_path", dc.DynamicVolPath).
		Str("volume_prefix", dc.VolumePrefix).Str("snapshot_prefix", dc.SnapshotPrefix).Str("seed_snapshot_prefix", dc.SnapshotPrefix).
		Bool("allow_auto_fs_creation", dc.allowAutoFsCreation).Bool("allow_auto_fs_expansion", dc.allowAutoFsExpansion).
		Bool("advertise_snapshot_support", dc.advertiseSnapshotSupport).Bool("advertise_volume_clone_support", dc.advertiseVolumeCloneSupport).
		Bool("advertise_volume_health_support", dc.advertiseVolumeHealthSupport).
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
		Dur("weka_api_timeout", dc.wekaApiTimeout).
		Int("health_probe_weka_timeout_seconds", int(dc.healthProbeWekaTimeout.Seconds())).
		Bool("allow_protocol_containers", dc.allowProtocolContainers).
		Bool("allow_nfs_failback", dc.allowNfsFailback).
		Bool("use_nfs", dc.useNfs).
		Str("interface_group_name", dc.interfaceGroupName).
		Str("client_group_name", dc.clientGroupName).
		Bool("skip_garbage_collection", dc.skipGarbageCollection).
		Bool("wait_for_object_deletion", dc.waitForObjectDeletion).
		Str("tracing_url", dc.tracingUrl).
		Bool("manage_node_topology_labels", dc.manageNodeTopologyLabels).
		Str("wekafs_container_name", dc.wekafsContainerName).
		Bool("enforce_dir_vol_total_capacity", dc.enforceDirVolTotalCapacity).
		Bool("set_ownership_on_dynamic_filesystems", dc.setOwnershipOnDynamicFilesystems).
		Bool("allow_mount_option_overrides", dc.allowMountOptionOverrides).
		Bool("keep_thin_provisioning_ratio_on_expand", dc.keepThinProvisioningRatioOnExpand).
		Dur("metrics_fetch_interval", dc.metricsFetchInterval).
		Int("metrics_fetch_concurrent_requests", dc.metricsFetchConcurrentRequests).
		Bool("enable_metrics_server_leader_election", dc.enableMetricsServerLeaderElection).
		Int("quota_fetch_concurrent_requests", dc.quotaFetchConcurrentRequests).
		Dur("quota_cache_validity_duration", dc.quotaCacheValidityDuration).
		Bool("use_quota_maps_for_metrics", dc.useQuotaMapsForMetrics).
		Msg("Starting driver with the following configuration")

}

// DriverConfigOptions carries the driver's startup settings into NewDriverConfig. Field names
// mirror the command line flags in cmd/wekafsplugin, so the two map one to one and a new setting
// is added in one obvious place. Every field is optional: the zero value is the "off" or
// "unspecified" case, except where NewDriverConfig documents a default.
type DriverConfigOptions struct {
	DynamicVolPath     string
	VolumePrefix       string
	SnapshotPrefix     string
	SeedSnapshotPrefix string
	DebugPath          string
	Version            string

	AllowAutoFsCreation              bool
	AllowAutoFsExpansion             bool
	AllowSnapshotsOfDirectoryVolumes bool
	AllowInsecureHttps               bool
	AlwaysAllowSnapshotVolumes       bool
	AllowProtocolContainers          bool
	AllowEncryptionWithoutKms        bool
	AllowMountOptionOverrides        bool

	// The Suppress fields mirror the negative CLI flags of the same name. NewDriverConfig turns
	// them into the positive advertise* settings the rest of the driver reads, so the inversion
	// lives in exactly one place.
	SuppressSnapshotSupport      bool
	SuppressVolumeCloneSupport   bool
	AdvertiseVolumeHealthSupport bool

	// MutuallyExclusiveMountOptions defaults to write cache / coherent / read cache when empty.
	MutuallyExclusiveMountOptions MutuallyExclusiveMountOptsStrings

	MaxCreateVolumeReqs        int64
	MaxDeleteVolumeReqs        int64
	MaxExpandVolumeReqs        int64
	MaxCreateSnapshotReqs      int64
	MaxDeleteSnapshotReqs      int64
	MaxNodePublishVolumeReqs   int64
	MaxNodeUnpublishVolumeReqs int64

	GrpcRequestTimeoutSeconds int
	// WekaApiTimeoutSeconds bounds a single Weka API request. Zero falls back to the API client's own
	// default, so a caller that does not care keeps today's behaviour.
	WekaApiTimeoutSeconds         int
	HealthProbeWekaTimeoutSeconds int

	AllowNfsFailback   bool
	UseNfs             bool
	InterfaceGroupName string
	ClientGroupName    string
	NfsProtocolVersion string

	SkipGarbageCollection             bool
	WaitForObjectDeletion             bool
	TracingUrl                        string
	ManageNodeTopologyLabels          bool
	WekafsContainerName               string
	EnforceDirVolTotalCapacity        bool
	SetOwnershipOnDynamicFilesystems  bool
	KeepThinProvisioningRatioOnExpand bool

	// MetricsFetchIntervalSeconds, MetricsFetchConcurrentRequests, QuotaFetchConcurrentRequests and
	// QuotaCacheValiditySeconds default to sensible non-zero values in NewDriverConfig when left at
	// zero, since zero would otherwise mean "never" or "unbounded".
	MetricsFetchIntervalSeconds       int
	MetricsFetchConcurrentRequests    int
	EnableMetricsServerLeaderElection bool
	QuotaFetchConcurrentRequests      int
	QuotaCacheValiditySeconds         int
	UseQuotaMapsForMetrics            bool
}

// Defaults applied by NewDriverConfig when the corresponding metrics server option is left at zero.
const (
	defaultMetricsFetchIntervalSeconds    = 60
	defaultMetricsFetchConcurrentRequests = 1
	defaultQuotaFetchConcurrentRequests   = 5
	defaultQuotaCacheValiditySeconds      = 60
)

func NewDriverConfig(opts DriverConfigOptions) *DriverConfig {
	var MutuallyExclusiveMountOptions []mutuallyExclusiveMountOptionSet
	for _, exclusiveSet := range opts.MutuallyExclusiveMountOptions {
		MutuallyExclusiveMountOptions = append(MutuallyExclusiveMountOptions, strings.Split(exclusiveSet, ","))
	}
	if len(MutuallyExclusiveMountOptions) == 0 {
		MutuallyExclusiveMountOptions = append(MutuallyExclusiveMountOptions, []string{MountOptionWriteCache, MountOptionCoherent, MountOptionReadCache})
	}

	concurrency := map[string]int64{
		"CreateVolume":        opts.MaxCreateVolumeReqs,
		"DeleteVolume":        opts.MaxDeleteVolumeReqs,
		"ExpandVolume":        opts.MaxExpandVolumeReqs,
		"CreateSnapshot":      opts.MaxCreateSnapshotReqs,
		"DeleteSnapshot":      opts.MaxDeleteSnapshotReqs,
		"NodePublishVolume":   opts.MaxNodePublishVolumeReqs,
		"NodeUnpublishVolume": opts.MaxNodeUnpublishVolumeReqs,
	}

	metricsFetchIntervalSeconds := opts.MetricsFetchIntervalSeconds
	if metricsFetchIntervalSeconds == 0 {
		metricsFetchIntervalSeconds = defaultMetricsFetchIntervalSeconds
	}
	metricsFetchConcurrentRequests := opts.MetricsFetchConcurrentRequests
	if metricsFetchConcurrentRequests == 0 {
		metricsFetchConcurrentRequests = defaultMetricsFetchConcurrentRequests
	}
	quotaFetchConcurrentRequests := opts.QuotaFetchConcurrentRequests
	if quotaFetchConcurrentRequests == 0 {
		quotaFetchConcurrentRequests = defaultQuotaFetchConcurrentRequests
	}
	quotaCacheValiditySeconds := opts.QuotaCacheValiditySeconds
	if quotaCacheValiditySeconds == 0 {
		quotaCacheValiditySeconds = defaultQuotaCacheValiditySeconds
	}

	return &DriverConfig{
		DynamicVolPath:                    opts.DynamicVolPath,
		VolumePrefix:                      opts.VolumePrefix,
		SnapshotPrefix:                    opts.SnapshotPrefix,
		SeedSnapshotPrefix:                opts.SeedSnapshotPrefix,
		allowAutoFsCreation:               opts.AllowAutoFsCreation,
		allowAutoFsExpansion:              opts.AllowAutoFsExpansion,
		allowSnapshotsOfDirectoryVolumes:  opts.AllowSnapshotsOfDirectoryVolumes,
		advertiseSnapshotSupport:          !opts.SuppressSnapshotSupport,
		advertiseVolumeCloneSupport:       !opts.SuppressVolumeCloneSupport,
		advertiseVolumeHealthSupport:      opts.AdvertiseVolumeHealthSupport,
		debugPath:                         opts.DebugPath,
		allowInsecureHttps:                opts.AllowInsecureHttps,
		alwaysAllowSnapshotVolumes:        opts.AlwaysAllowSnapshotVolumes,
		mutuallyExclusiveOptions:          MutuallyExclusiveMountOptions,
		maxConcurrencyPerOp:               concurrency,
		grpcRequestTimeout:                time.Duration(opts.GrpcRequestTimeoutSeconds) * time.Second,
		wekaApiTimeout:                    time.Duration(opts.WekaApiTimeoutSeconds) * time.Second,
		healthProbeWekaTimeout:            time.Duration(opts.HealthProbeWekaTimeoutSeconds) * time.Second,
		allowProtocolContainers:           opts.AllowProtocolContainers,
		allowNfsFailback:                  opts.AllowNfsFailback,
		useNfs:                            opts.UseNfs,
		interfaceGroupName:                opts.InterfaceGroupName,
		clientGroupName:                   opts.ClientGroupName,
		nfsProtocolVersion:                opts.NfsProtocolVersion,
		csiVersion:                        opts.Version,
		skipGarbageCollection:             opts.SkipGarbageCollection,
		waitForObjectDeletion:             opts.WaitForObjectDeletion,
		allowEncryptionWithoutKms:         opts.AllowEncryptionWithoutKms,
		tracingUrl:                        opts.TracingUrl,
		manageNodeTopologyLabels:          opts.ManageNodeTopologyLabels,
		wekafsContainerName:               opts.WekafsContainerName,
		enforceDirVolTotalCapacity:        opts.EnforceDirVolTotalCapacity,
		setOwnershipOnDynamicFilesystems:  opts.SetOwnershipOnDynamicFilesystems,
		allowMountOptionOverrides:         opts.AllowMountOptionOverrides,
		keepThinProvisioningRatioOnExpand: opts.KeepThinProvisioningRatioOnExpand,

		metricsFetchInterval:              time.Duration(metricsFetchIntervalSeconds) * time.Second,
		metricsFetchConcurrentRequests:    metricsFetchConcurrentRequests,
		enableMetricsServerLeaderElection: opts.EnableMetricsServerLeaderElection,
		quotaFetchConcurrentRequests:      quotaFetchConcurrentRequests,
		quotaCacheValidityDuration:        time.Duration(quotaCacheValiditySeconds) * time.Second,
		useQuotaMapsForMetrics:            opts.UseQuotaMapsForMetrics,
	}
}

func (dc *DriverConfig) isInDevMode() bool {
	return dc.debugPath != ""
}

// requiresPvCaching reports whether any enabled feature reads PersistentVolumes, and hence whether
// the controller-runtime cache should be set up to hold them. Capacity enforcement lists them to
// total up provisioned capacity, and volume health reporting resolves a CSI volume handle back to
// its PersistentVolume. With neither enabled nothing lists PVs and no informer is started.
func (dc *DriverConfig) requiresPvCaching() bool {
	return dc.enforceDirVolTotalCapacity || dc.advertiseVolumeHealthSupport
}

func (dc *DriverConfig) GetVersion() string {
	return dc.csiVersion
}

func (dc *DriverConfig) SetDriver(driver *WekaFsDriver) {
	dc.driverRef = driver
}

func (dc *DriverConfig) GetDriver() *WekaFsDriver {
	return dc.driverRef
}
