package apiclient

import (
	"github.com/hashicorp/go-version"
	"github.com/rs/zerolog/log"
)

type WekaCompatibilityRequiredVersions struct {
	FilesystemAsVolume             string
	DirectoryAsCSIVolume           string
	QuotaDirectoryAsVolume         string
	QuotaOnNonEmptyDirs            string
	QuotaOnSnapshot                string
	MountFilesystemsUsingAuthToken string
	RestApiFiltering               string
	NewFilesystemFromSnapshot      string
	CloneFilesystem                string
	UrlQueryParams                 string
	SyncOnCloseMountOption         string
}

var MinimumSupportedWekaVersions = &WekaCompatibilityRequiredVersions{
	DirectoryAsCSIVolume:           "v3.0",  // can create CSI volume from directory, without quota support
	FilesystemAsVolume:             "v3.13", // can create CSI volume from filesystem
	QuotaDirectoryAsVolume:         "v3.13", // can create CSI volume from directory with quota support
	QuotaOnNonEmptyDirs:            "v9.99", // can enable quota on legacy CSI volume (directory) without quota support
	QuotaOnSnapshot:                "v4.1",  // can create a valid quota on snapshot
	MountFilesystemsUsingAuthToken: "v3.14", // can mount filesystems that require authentication (and non-root orgID)
	RestApiFiltering:               "v9.99", // can filter API objects by query
	NewFilesystemFromSnapshot:      "v9.99", // can create new filesystem from snapshot on storage side
	CloneFilesystem:                "v9.99", // can clone a volume directly on storage side
	UrlQueryParams:                 "v4.1",  // can perform URL query by fields
	SyncOnCloseMountOption:         "v4.2",  // can perform sync_on_close_mount_option
}

type WekaCompatibilityMap struct {
	FilesystemAsCSIVolume           bool
	DirectoryAsCSIVolume            bool
	QuotaOnDirectoryVolume          bool
	QuotaSetOnNonEmptyVolume        bool
	QuotaOnSnapshot                 bool
	MountFilesystemsUsingAuthToken  bool
	FilterRestApiRequests           bool
	CreateNewFilesystemFromSnapshot bool
	CloneFilesystem                 bool
	UrlQueryParams                  bool
	SyncOnCloseMountOption          bool
}

func (cm *WekaCompatibilityMap) fillIn(versionStr string) {
	v, err := version.NewVersion(versionStr)
	if err != nil {
		log.Error().Err(err).Str("cluster_version_string", versionStr).Msg("Could not parse cluster version")
		cm.DirectoryAsCSIVolume = true
		cm.FilesystemAsCSIVolume = false
		cm.QuotaOnDirectoryVolume = false
		cm.QuotaSetOnNonEmptyVolume = false
		cm.MountFilesystemsUsingAuthToken = false
		cm.FilterRestApiRequests = false
		cm.CreateNewFilesystemFromSnapshot = false
		cm.CloneFilesystem = false
		cm.QuotaOnSnapshot = false
		cm.UrlQueryParams = false
		cm.SyncOnCloseMountOption = false
		return
	}
	d, _ := version.NewVersion(MinimumSupportedWekaVersions.DirectoryAsCSIVolume)
	f, _ := version.NewVersion(MinimumSupportedWekaVersions.FilesystemAsVolume)
	q, _ := version.NewVersion(MinimumSupportedWekaVersions.QuotaDirectoryAsVolume)
	n, _ := version.NewVersion(MinimumSupportedWekaVersions.QuotaOnNonEmptyDirs)
	a, _ := version.NewVersion(MinimumSupportedWekaVersions.MountFilesystemsUsingAuthToken)
	r, _ := version.NewVersion(MinimumSupportedWekaVersions.RestApiFiltering)
	s, _ := version.NewVersion(MinimumSupportedWekaVersions.NewFilesystemFromSnapshot)
	c, _ := version.NewVersion(MinimumSupportedWekaVersions.CloneFilesystem)
	qs, _ := version.NewVersion(MinimumSupportedWekaVersions.QuotaOnSnapshot)
	u, _ := version.NewVersion(MinimumSupportedWekaVersions.UrlQueryParams)
	sc, _ := version.NewVersion(MinimumSupportedWekaVersions.SyncOnCloseMountOption)

	cm.DirectoryAsCSIVolume = v.GreaterThanOrEqual(d)
	cm.FilesystemAsCSIVolume = v.GreaterThanOrEqual(f)
	cm.QuotaOnDirectoryVolume = v.GreaterThanOrEqual(q)
	cm.QuotaSetOnNonEmptyVolume = v.GreaterThanOrEqual(n)
	cm.MountFilesystemsUsingAuthToken = v.GreaterThanOrEqual(a)
	cm.FilterRestApiRequests = v.GreaterThanOrEqual(r)
	cm.CreateNewFilesystemFromSnapshot = v.GreaterThanOrEqual(s)
	cm.CloneFilesystem = v.GreaterThanOrEqual(c)
	cm.QuotaOnSnapshot = v.GreaterThanOrEqual(qs)
	cm.UrlQueryParams = v.GreaterThanOrEqual(u)
	cm.SyncOnCloseMountOption = v.GreaterThanOrEqual(sc)
}

func (a *ApiClient) SupportsQuotaDirectoryAsVolume() bool {
	return a.CompatibilityMap.QuotaOnDirectoryVolume
}

func (a *ApiClient) SupportsQuotaOnNonEmptyDirs() bool {
	return a.CompatibilityMap.QuotaSetOnNonEmptyVolume
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
