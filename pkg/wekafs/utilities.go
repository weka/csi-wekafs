package wekafs

import (
	"crypto/sha1"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ProduceVolumeIdFromRequest(req *csi.CreateVolumeRequest) string {
	name := req.GetName()

	var volId string
	if req.GetParameters()["volumeType"] == "wekafs_dirquota" {
		// we have a dirquota in request
		filesystemName := ObtainFilesystemNameFromRequest(req)
		h := sha1.New()
		h.Write([]byte(name))
		hash := string(h.Sum(nil))
		volId = filesystemName + "/" + hash + "/" + name
	} else if req.GetParameters()["volumeType"] == "wekafs_filesystem" {
		volId = "kube-" + name
	}
	return TruncateString(volId, maxDirectoryNameLength)
}

func ObtainFilesystemNameFromRequest(req *csi.CreateVolumeRequest) string {
	var filesystemName string
	if req.GetParameters()["filesystem_name"] == "" {
		filesystemName = defaultFilesystemName
	} else {
		filesystemName = req.GetParameters()["filesystem_name"]
	}
	return filesystemName
}

func ObtainFilesystemNameFromVolumeId(volumeID string) string {
	slices := strings.Split(volumeID, "/")
	return slices[0]
}

func ObtainVolumeNameFromId(volumeID string) string {
	slices := strings.Split(volumeID, "/")
	if len(slices) > 1 {
		// this is dirquota
		return strings.Join(slices[2:], "/")
	}
	// this is filesystem
	return slices[0]
}

func ObtainDirQuotaNameFromVolumeId(volumeID string) string {
	slices := strings.Split(volumeID, "/")
	if len(slices) > 1 {
		// this is dirquota
		return strings.Join(slices[1:], "/")
	}
	// this is filesystem
	return ""
}

func ObtainVolumeTypeFromVolumeId(volumeID string) string {
	slices := strings.Split(volumeID, "/")
	if len(slices) > 1 {
		return "wekafs_dirquota"
	}
	return "wekafs_filesystem"
}

func TruncateString(s string, i int) string {
	runes := []rune( s )
	if len(runes) > i {
		return string(runes[:i])
	}
	return s
}

func getVolumeByID(volumeID string) (wekaFsVolume, error) {
	volPath := getVolumePath(volumeID)

	if DirectoryExists(volPath) {
		return wekaFsVolume{
			VolName:        ObtainVolumeNameFromId(volumeID),
			VolID:          volumeID,
			VolSize:        0, //TODO: Extract size from xattr
			VolPath:        volPath,
			Ephemeral:      false,
			FilesystemName: ObtainFilesystemNameFromVolumeId(volumeID),
			DirQuotaName:   ObtainDirQuotaNameFromVolumeId(volumeID),
			VolumeType:     ObtainVolumeTypeFromVolumeId(volumeID),
		}, nil
	} else {
		return wekaFsVolume{}, fmt.Errorf("volume id %s does not exist in the volumes list", volumeID)
	}
}

// getVolumePath returns the canonical path for wekafs_dirqouta
//volume
func getVolumePath(volID string) string {
	return getDriverMountPath("driver-coherent", volID)
}

func getDriverMountPath(mountMode, volID string) string {
	return filepath.Join(dataRoot, mountMode, volID)
}

func getFilesystemPath(filesystemName string) string {
	return filepath.Join(dataRoot, "driver-coherent", filesystemName)
}

// VolumeIsEmpty is a simple check to determine if the specified wekafs_dirqouta directory
// is empty or not.
func VolumeIsEmpty(p string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return true, fmt.Errorf("unable to open wekafs_dirqouta"+
			" volume, error: %v", err)
	}
	defer f.Close()

	_, err = f.Readdir(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

