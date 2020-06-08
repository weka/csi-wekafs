package wekafs

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func createWekafsVolumeRouter(volumeType string, volID, name string, cap int64, ephemeral bool) (*wekaFsVolume, error) {
	switch volumeType {
	case "wekafs_dirquota":
		return createWekafsDirquota(volID, name, cap, ephemeral)
	case "wekafs_filesystem":
		return createWekafsFilesystem(volID, name, cap, ephemeral)
	}
	return nil, status.Errorf(codes.InvalidArgument, "invalid volumeType specified in request")
}

func deleteWekaFsVolumeRouter(volID string) error {
	volumeType := ObtainVolumeTypeFromVolumeId(volID)
	switch volumeType {
	case "wekafs_dirquota":
		return deleteDirquotaVolume(volID)
	case "wekafs_filesystem":
		return deleteWekaFilesystem(volID)
	}
	return status.Errorf(codes.InvalidArgument, "unsupported volumeId")
}

func updateWekaFsVolumeRouter(volID string, volume wekaFsVolume) error {
	volumeType := ObtainVolumeTypeFromVolumeId(volID)
	switch volumeType {
	case "wekafs_dirquota":
		return updateDirQuotaVolume(volID, volume)

	case "wekafs_filesystem":
		return updateWekaFilesystem(volID, volume)
	}
	return status.Errorf(codes.InvalidArgument, "unsupported volumeId")
}

func GetMaxCapacityRouter(volID string) (int64, error) {
	volumeType := ObtainVolumeTypeFromVolumeId(volID)
	switch volumeType {
	case "wekafs_dirquota":
		filesystemName := ObtainFilesystemNameFromVolumeId(volID)
		return GetMaxDirQuotaCapacity(filesystemName)
	case "wekafs_filesystem":
		return GetMaxCapacityForNewFilesystem()
	}
	return -1, status.Errorf(codes.InvalidArgument, "unsupported volumeId")
}
