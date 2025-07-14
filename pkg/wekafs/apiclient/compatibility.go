package apiclient

import (
	"github.com/hashicorp/go-version"
	"github.com/rs/zerolog/log"
)

type WekaCompatibilityRequiredVersions struct {
	FilesystemAsVolume               string
	DirectoryAsCSIVolume             string
	QuotaDirectoryAsVolume           string
	QuotaOnNonEmptyDirs              string
	QuotaOnSnapshot                  string
	MountFilesystemsUsingAuthToken   string
	NewFilesystemFromSnapshot        string
	CloneFilesystem                  string
	UrlQueryParams                   string
	SyncOnCloseMountOption           string
	SingleClientMultipleClusters     string
	NewNodeApiObjectPath             string
	EncryptionWithNoKms              string
	EncryptionWithClusterKey         string
	EncryptionWithCustomSettings     string
	ResolvePathToInode               string
	ResolvePathToInodeCsiRole        string
	GetPerFilesystemPerformanceStats string
	GetPerQuotaPerformanceStats      string
}

var MinimumSupportedWekaVersions = &WekaCompatibilityRequiredVersions{
	DirectoryAsCSIVolume:             "v3.0",   // can create CSI volume from directory, without quota support
	FilesystemAsVolume:               "v3.13",  // can create CSI volume from filesystem
	QuotaDirectoryAsVolume:           "v3.13",  // can create CSI volume from directory with quota support
	QuotaOnSnapshot:                  "v4.2",   // can create a valid quota on snapshot
	MountFilesystemsUsingAuthToken:   "v3.14",  // can mount filesystems that require authentication (and non-root orgID)
	NewFilesystemFromSnapshot:        "v9.99",  // can create new filesystem from snapshot on storage side
	CloneFilesystem:                  "v9.99",  // can clone a volume directly on storage side
	UrlQueryParams:                   "v4.0",   // can perform URL query by fields
	SyncOnCloseMountOption:           "v4.2",   // can perform sync_on_close mount option
	SingleClientMultipleClusters:     "v4.2",   // single client can have multiple Weka cluster connections
	NewNodeApiObjectPath:             "v4.2",   // new API object paths (processes, containers, etc.)
	EncryptionWithNoKms:              "v4.0",   // can create encrypted filesystems without KMS
	EncryptionWithClusterKey:         "v4.0",   // can create encrypted filesystems with common cluster-wide key
	EncryptionWithCustomSettings:     "v4.4.1", // can create encrypted filesystems with custom settings (key per filesystem(s))
	ResolvePathToInode:               "v4.3",   // can resolve a path to an inode instead of doing it via mount
	ResolvePathToInodeCsiRole:        "v4.4.7", // can resolve a path to an inode via API with CSI role
	GetPerFilesystemPerformanceStats: "v4.4.6", // can get per filesystem performance stats
	GetPerQuotaPerformanceStats:      "v9.99",  // can get per quota performance stats
}

type WekaCompatibilityMap struct {
	FilesystemAsCSIVolume            bool
	DirectoryAsCSIVolume             bool
	QuotaOnDirectoryVolume           bool
	QuotaOnSnapshot                  bool
	MountFilesystemsUsingAuthToken   bool
	CreateNewFilesystemFromSnapshot  bool
	CloneFilesystem                  bool
	UrlQueryParams                   bool
	SyncOnCloseMountOption           bool
	SingleClientMultipleClusters     bool
	NewNodeApiObjectPath             bool
	EncryptionWithNoKms              bool
	EncryptionWithClusterKey         bool
	EncryptionWithCustomSettings     bool
	ResolvePathToInode               bool
	ResolvePathToInodeCsiRole        bool
	GetPerFilesystemPerformanceStats bool
	GetPerQuotaPerformanceStats      bool
}

func (cm *WekaCompatibilityMap) fillIn(versionStr string) {
	v, err := version.NewVersion(versionStr)
	if err != nil {
		log.Error().Err(err).Str("cluster_version_string", versionStr).Msg("Could not parse cluster version")
		cm.DirectoryAsCSIVolume = true
		cm.FilesystemAsCSIVolume = false
		cm.QuotaOnDirectoryVolume = false
		cm.MountFilesystemsUsingAuthToken = false
		cm.CreateNewFilesystemFromSnapshot = false
		cm.CloneFilesystem = false
		cm.QuotaOnSnapshot = false
		cm.UrlQueryParams = false
		cm.SyncOnCloseMountOption = false
		cm.SingleClientMultipleClusters = false
		cm.NewNodeApiObjectPath = false
		cm.EncryptionWithNoKms = false
		cm.EncryptionWithClusterKey = false
		cm.EncryptionWithCustomSettings = false
		cm.ResolvePathToInode = false
		cm.ResolvePathToInodeCsiRole = false
		cm.GetPerQuotaPerformanceStats = false

		return
	}
	d, _ := version.NewVersion(MinimumSupportedWekaVersions.DirectoryAsCSIVolume)
	f, _ := version.NewVersion(MinimumSupportedWekaVersions.FilesystemAsVolume)
	q, _ := version.NewVersion(MinimumSupportedWekaVersions.QuotaDirectoryAsVolume)
	a, _ := version.NewVersion(MinimumSupportedWekaVersions.MountFilesystemsUsingAuthToken)
	s, _ := version.NewVersion(MinimumSupportedWekaVersions.NewFilesystemFromSnapshot)
	c, _ := version.NewVersion(MinimumSupportedWekaVersions.CloneFilesystem)
	qs, _ := version.NewVersion(MinimumSupportedWekaVersions.QuotaOnSnapshot)
	u, _ := version.NewVersion(MinimumSupportedWekaVersions.UrlQueryParams)
	sc, _ := version.NewVersion(MinimumSupportedWekaVersions.SyncOnCloseMountOption)
	mc, _ := version.NewVersion(MinimumSupportedWekaVersions.SingleClientMultipleClusters)
	nn, _ := version.NewVersion(MinimumSupportedWekaVersions.NewNodeApiObjectPath)
	en, _ := version.NewVersion(MinimumSupportedWekaVersions.EncryptionWithNoKms)
	ec, _ := version.NewVersion(MinimumSupportedWekaVersions.EncryptionWithClusterKey)
	ecc, _ := version.NewVersion(MinimumSupportedWekaVersions.EncryptionWithCustomSettings)
	rp, _ := version.NewVersion(MinimumSupportedWekaVersions.ResolvePathToInode)
	rpc, _ := version.NewVersion(MinimumSupportedWekaVersions.ResolvePathToInodeCsiRole)
	qps, _ := version.NewVersion(MinimumSupportedWekaVersions.GetPerQuotaPerformanceStats)
	fps, _ := version.NewVersion(MinimumSupportedWekaVersions.GetPerFilesystemPerformanceStats)

	cm.DirectoryAsCSIVolume = v.GreaterThanOrEqual(d)
	cm.FilesystemAsCSIVolume = v.GreaterThanOrEqual(f)
	cm.QuotaOnDirectoryVolume = v.GreaterThanOrEqual(q)
	cm.MountFilesystemsUsingAuthToken = v.GreaterThanOrEqual(a)
	cm.CreateNewFilesystemFromSnapshot = v.GreaterThanOrEqual(s)
	cm.CloneFilesystem = v.GreaterThanOrEqual(c)
	cm.QuotaOnSnapshot = v.GreaterThanOrEqual(qs)
	cm.UrlQueryParams = v.GreaterThanOrEqual(u)
	cm.SyncOnCloseMountOption = v.GreaterThanOrEqual(sc)
	cm.SingleClientMultipleClusters = v.GreaterThanOrEqual(mc)
	cm.NewNodeApiObjectPath = v.GreaterThanOrEqual(nn)
	cm.EncryptionWithNoKms = v.GreaterThanOrEqual(en)
	cm.EncryptionWithClusterKey = v.GreaterThanOrEqual(ec)
	cm.EncryptionWithCustomSettings = v.GreaterThanOrEqual(ecc)
	cm.ResolvePathToInode = v.GreaterThanOrEqual(rp)
	cm.ResolvePathToInodeCsiRole = v.GreaterThanOrEqual(rpc)
	cm.GetPerFilesystemPerformanceStats = v.GreaterThanOrEqual(fps)
	cm.GetPerQuotaPerformanceStats = v.GreaterThanOrEqual(qps)
}

func (a *ApiClient) SupportsQuotaDirectoryAsVolume() bool {
	return a.CompatibilityMap.QuotaOnDirectoryVolume
}

func (a *ApiClient) SupportsQuotaOnSnapshots() bool {
	return a.CompatibilityMap.QuotaOnSnapshot
}

func (a *ApiClient) SupportsFilesystemAsVolume() bool {
	return a.CompatibilityMap.FilesystemAsCSIVolume
}

func (a *ApiClient) SupportsDirectoryAsVolume() bool {
	return a.CompatibilityMap.DirectoryAsCSIVolume
}

func (a *ApiClient) SupportsAuthenticatedMounts() bool {
	return a.CompatibilityMap.MountFilesystemsUsingAuthToken
}

func (a *ApiClient) SupportsFilesystemCloning() bool {
	return a.CompatibilityMap.CloneFilesystem
}

func (a *ApiClient) SupportsNewFileSystemFromSnapshot() bool {
	return a.CompatibilityMap.CreateNewFilesystemFromSnapshot
}

func (a *ApiClient) SupportsUrlQueryParams() bool {
	return a.CompatibilityMap.UrlQueryParams
}

func (a *ApiClient) SupportsSyncOnCloseMountOption() bool {
	return a.CompatibilityMap.SyncOnCloseMountOption
}

func (a *ApiClient) SupportsMultipleClusters() bool {
	return a.CompatibilityMap.SingleClientMultipleClusters
}

func (a *ApiClient) SupportsEncryptionWithNoKms() bool {
	return a.CompatibilityMap.EncryptionWithNoKms
}

func (a *ApiClient) SupportsEncryptionWithCommonKey() bool {
	return a.CompatibilityMap.EncryptionWithClusterKey
}

func (a *ApiClient) SupportsCustomEncryptionSettings() bool {
	return a.CompatibilityMap.EncryptionWithCustomSettings
}

func (a *ApiClient) RequiresNewNodePath() bool {
	return a.CompatibilityMap.NewNodeApiObjectPath
}

func (a *ApiClient) SupportsResolvePathToInode() bool {
	if !a.CompatibilityMap.ResolvePathToInode {
		return false
	}
	if a.ApiUserRole == "" {
		return false
	}
	if a.ApiUserRole == ApiUserRoleCSI {
		return a.CompatibilityMap.ResolvePathToInodeCsiRole
	}
	return true
}

func (a *ApiClient) SupportsPerFilesystemPerformanceStats() bool {
	return a.CompatibilityMap.GetPerFilesystemPerformanceStats
}

func (a *ApiClient) SupportsPerVolumePerformanceStats() bool {
	return a.CompatibilityMap.GetPerQuotaPerformanceStats
}
