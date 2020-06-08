package wekafs

import (
	"fmt"
	"github.com/golang/glog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"path"
	"syscall"
)

func createWekafsDirquota(volID, name string, cap int64, ephemeral bool) (*wekaFsVolume, error) {
	filesystemName := ObtainFilesystemNameFromVolumeId(volID)
	volumePath := getVolumePath(volID)
	filesystemPath := getFilesystemPath(filesystemName)

	if _, err := os.Stat(filesystemPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to access Weka filesystem: %v, %v", filesystemName, err)
	}

	err := os.MkdirAll(volID, 0777)
	if err != nil {
		return nil, err
	}

	dirQuotaVol := wekaFsVolume{
		VolName:        name,
		VolID:          volID,
		VolSize:        cap,
		VolPath:        volumePath,
		Ephemeral:      ephemeral,
		FilesystemName: filesystemName,
		DirQuotaName:   ObtainDirQuotaNameFromVolumeId(volID),
		VolumeType:     "wekafs_dirquota",
	}
	return &dirQuotaVol, nil
}

func GetMaxDirQuotaCapacity(filesystem string) (int64, error) {

	var stat syscall.Statfs_t
	wd := getFilesystemPath(filesystem)

	err := syscall.Statfs(wd, &stat)
	if err != nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not obtain free capacity on filesystem %s", filesystem)
	}

	// Available blocks * size per block = available space in bytes
	return int64(stat.Bavail * uint64(stat.Bsize)), nil
}

func deleteDirquotaVolume(volID string) error {
	glog.V(4).Infof("deleting wekafs_dirqouta"+
		" volume: %s", volID)

	_, err := getVolumeByID(volID)
	if err != nil {
		// Return OK if the volume is not found.
		return nil
	}

	path := getVolumePath(volID)
	if err := dirQuotaAsyncDelete(path); err != nil {
		return fmt.Errorf("could not remove volume %s", volID)
	}
	return nil
}

// updateVolume updates the existing wekafs_dirqouta
//volume.
func updateDirQuotaVolume(volID string, volume wekaFsVolume) error {
	glog.V(4).Infof("updating wekafs volume: %s", volID)

	if _, err := getVolumeByID(volID); err != nil {
		return err
	}
	//TODO: implement xattrs modification here. Function will be used also internally
	return nil
}

func dirQuotaAsyncDelete(volID string) error {
	filesystemName := ObtainFilesystemNameFromVolumeId(volID)
	filesystemPath := getFilesystemPath(filesystemName)
	garbageCollectionPath := path.Join(filesystemPath, ".delete")
	volumePath := getVolumePath(volID)
	os.Rename(volumePath, garbageCollectionPath)
	return nil
}

