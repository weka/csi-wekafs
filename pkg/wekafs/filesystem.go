package wekafs

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func createWekafsFilesystem(volID, name string, cap int64, ephemeral bool) (*wekaFsVolume, error) {
	//volumePath := path.Join(dataRoot, volID)
	//return &wekaFsVolume{
	//	VolName: name,
	//	VolID: volID,
	//	VolSize: cap,
	//	VolPath: volumePath,
	//	Ephemeral: ephemeral,
	//	FilesystemName: volID,
	//	DirQuotaName: "",
	//	VolumeType: "wekafs_filesystem",
	//}, nil
	return &wekaFsVolume{}, status.Errorf(codes.Unimplemented, "Currently not implemented")
}

func deleteWekaFilesystem(volID string) error {
	return status.Errorf(codes.Unimplemented, "Currently not implemented")
}

func updateWekaFilesystem(volID string, volume wekaFsVolume) error {
	return status.Errorf(codes.Unimplemented, "Currently not implemented")
}

func GetMaxCapacityForNewFilesystem() (int64, error) {
	panic("implement me")
	//TODO: implement via API in order to obtain new FS max size
}

