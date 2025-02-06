package apiclient

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWekaCompatibilityMap_fillIn(t *testing.T) {
	tests := []struct {
		versionStr string
		expected   WekaCompatibilityMap
	}{
		{
			versionStr: "v3.0",
			expected: WekaCompatibilityMap{
				DirectoryAsCSIVolume: true,
			},
		},
		{
			versionStr: "v3.13",
			expected: WekaCompatibilityMap{
				DirectoryAsCSIVolume:   true,
				FilesystemAsCSIVolume:  true,
				QuotaOnDirectoryVolume: true,
			},
		},

		{
			versionStr: "v3.14",
			expected: WekaCompatibilityMap{
				DirectoryAsCSIVolume:           true,
				FilesystemAsCSIVolume:          true,
				QuotaOnDirectoryVolume:         true,
				MountFilesystemsUsingAuthToken: true,
			},
		},
		{
			versionStr: "v4.2",
			expected: WekaCompatibilityMap{
				DirectoryAsCSIVolume:           true,
				FilesystemAsCSIVolume:          true,
				QuotaOnDirectoryVolume:         true,
				QuotaOnSnapshot:                true,
				UrlQueryParams:                 true,
				SyncOnCloseMountOption:         true,
				SingleClientMultipleClusters:   true,
				NewNodeApiObjectPath:           true,
				MountFilesystemsUsingAuthToken: true,
			},
		},
		{
			versionStr: "v4.2.92.16",
			expected: WekaCompatibilityMap{
				DirectoryAsCSIVolume:           true,
				FilesystemAsCSIVolume:          true,
				QuotaOnDirectoryVolume:         true,
				QuotaOnSnapshot:                true,
				UrlQueryParams:                 true,
				SyncOnCloseMountOption:         true,
				SingleClientMultipleClusters:   true,
				NewNodeApiObjectPath:           true,
				MountFilesystemsUsingAuthToken: true,
			},
		},
		{
			versionStr: "v9.99",
			expected: WekaCompatibilityMap{
				DirectoryAsCSIVolume:            true,
				FilesystemAsCSIVolume:           true,
				QuotaOnDirectoryVolume:          true,
				QuotaOnSnapshot:                 true,
				MountFilesystemsUsingAuthToken:  true,
				CreateNewFilesystemFromSnapshot: true,
				CloneFilesystem:                 true,
				UrlQueryParams:                  true,
				SyncOnCloseMountOption:          true,
				SingleClientMultipleClusters:    true,
				NewNodeApiObjectPath:            true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.versionStr, func(t *testing.T) {
			var cm WekaCompatibilityMap
			cm.fillIn(tt.versionStr)
			assert.Equal(t, tt.expected, cm)
		})
	}
}
