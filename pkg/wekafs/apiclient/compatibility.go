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
	SetSelfAsFilesystemOwnerOnCreate string
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
	SetSelfAsFilesystemOwnerOnCreate: "v5.1",   // CSI can create filesystems while setting itself as explicit filesystem owner
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
	SetSelfAsFilesystemOwnerOnCreate bool
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
		cm.SetSelfAsFilesystemOwnerOnCreate = false

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
	sfo, _ := version.NewVersion(MinimumSupportedWekaVersions.SetSelfAsFilesystemOwnerOnCreate)

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
	cm.SetSelfAsFilesystemOwnerOnCreate = v.GreaterThanOrEqual(sfo)
}

// compatibility returns the compatibility map established at login. Reads go through here because
// fetchClusterInfo replaces it on every login, concurrently with requests that consult it.
func (a *ApiClient) compatibility() *WekaCompatibilityMap {
	a.RLock()
	defer a.RUnlock()
	return a.CompatibilityMap
}

func (a *ApiClient) SupportsQuotaDirectoryAsVolume() bool {
	return a.compatibility().QuotaOnDirectoryVolume
}

func (a *ApiClient) SupportsQuotaOnSnapshots() bool {
	return a.compatibility().QuotaOnSnapshot
}

func (a *ApiClient) SupportsFilesystemAsVolume() bool {
	return a.compatibility().FilesystemAsCSIVolume
}

func (a *ApiClient) SupportsDirectoryAsVolume() bool {
	return a.compatibility().DirectoryAsCSIVolume
}

func (a *ApiClient) SupportsAuthenticatedMounts() bool {
	return a.compatibility().MountFilesystemsUsingAuthToken
}

func (a *ApiClient) SupportsFilesystemCloning() bool {
	return a.compatibility().CloneFilesystem
}

func (a *ApiClient) SupportsNewFileSystemFromSnapshot() bool {
	return a.compatibility().CreateNewFilesystemFromSnapshot
}

func (a *ApiClient) SupportsUrlQueryParams() bool {
	return a.compatibility().UrlQueryParams
}

func (a *ApiClient) SupportsSyncOnCloseMountOption() bool {
	return a.compatibility().SyncOnCloseMountOption
}

func (a *ApiClient) SupportsMultipleClusters() bool {
	return a.compatibility().SingleClientMultipleClusters
}

func (a *ApiClient) SupportsEncryptionWithNoKms() bool {
	return a.compatibility().EncryptionWithNoKms
}

func (a *ApiClient) SupportsEncryptionWithCommonKey() bool {
	return a.compatibility().EncryptionWithClusterKey
}

func (a *ApiClient) SupportsCustomEncryptionSettings() bool {
	return a.compatibility().EncryptionWithCustomSettings
}

func (a *ApiClient) RequiresNewNodePath() bool {
	return a.compatibility().NewNodeApiObjectPath
}

func (a *ApiClient) SupportsResolvePathToInode() bool {
	if !a.compatibility().ResolvePathToInode {
		return false
	}
	role := a.userRole()
	if role == "" {
		return false
	}
	if role == ApiUserRoleCSI {
		return a.compatibility().ResolvePathToInodeCsiRole
	}
	return true
}

func (a *ApiClient) SupportsSettingSelfAsFilesystemOwner() bool {
	return a.compatibility().SetSelfAsFilesystemOwnerOnCreate
}
