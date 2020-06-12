package wekafs

import (
	"github.com/golang/glog"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"path/filepath"
	"syscall"
)

type dirVolume struct {
	id         string
	fs         string
	volumeType string
	dirName    string
}

func (v dirVolume) moveToTrash(mounter *wekaMounter, gc *dirVolumeGc) error {
	mountPath, err, unmount := mounter.Mount(v.fs)
	defer unmount()
	if err != nil {
		glog.Errorf("Error mounting %s for deletion %s", v.id, err)
		return err
	}

	err = os.MkdirAll(filepath.Join(mountPath, garbagePath), 0750)
	if err != nil {
		return err
	}
	u,_ := uuid.NewUUID()
	volumeTrashLoc :=  filepath.Join(mountPath, garbagePath, u.String())
	if err = os.Rename(v.getFullPath(mountPath), volumeTrashLoc);err==nil{
		v.dirName = u.String()
		gc.triggerGcVolume(v) // TODO: Better to preserve immutability some way , needed due to recreation of volumes with same name
		glog.V(4).Infof("Moved %s to trash", v.id)
		return err
	}else{
		glog.V(4).Infof("Failed moving %s to trash: %s", v.getFullPath(mountPath), err)
		return err
	}
}

func (v dirVolume) getFullPath(mountPath string) string {
	return filepath.Join(mountPath, v.dirName)
}

func NewVolume(volumeId string) (dirVolume, error) {
	if err := validateVolumeId(volumeId); err != nil {
		return dirVolume{}, err
	}
	return dirVolume{
		id:         volumeId,
		fs:         GetFSName(volumeId),
		volumeType: GetVolumeType(volumeId),
		dirName:    GetVolumeDirName(volumeId),
	}, nil
}

func getMaxDirCapacity(mountPath string) (int64, error) {

	var stat syscall.Statfs_t
	err := syscall.Statfs(mountPath, &stat)
	if err != nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not obtain free capacity on mount path %s", mountPath)
	}
	// Available blocks * size per block = available space in bytes
	maxCapacity := int64(stat.Bavail * uint64(stat.Bsize))
	return maxCapacity, nil
}

func updateDirCapacity(volumePath string, capacity int64) error {
	glog.V(4).Infof("updating wekafs volume: %s", volumePath)
	m := make(map[string][]byte)
	m[xattrCapacity] = []byte(string(capacity))
	return updateXattrs(volumePath, m)
}
