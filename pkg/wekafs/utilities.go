package wekafs

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"
	"github.com/pkg/xattr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func createVolumeIdFromRequest(req *csi.CreateVolumeRequest, dynamicVolPath string) (string, error) {
	name := req.GetName()

	var volId string
	volType := req.GetParameters()["volumeType"]

	switch volType {
	case VolumeTypeDirV1:
		// we have a dir in request or no info
		filesystemName := GetFSNameFromRequest(req)
		asciiPart := getAsciiPart(name, 64)
		hash := getStringSha1(name)
		folderName := asciiPart + "-" + hash
		if dynamicVolPath != "" {
			volId = filepath.Join(volType, filesystemName, dynamicVolPath, folderName)
		} else {
			volId = filepath.Join(volType, filesystemName, folderName)
		}
		return volId, nil

	case "":
		return "", status.Errorf(codes.InvalidArgument, "missing VolumeType in CreateVolumeRequest")

	default:
		exitMsg := "Unsupported volumeType in CreateVolumeRequest"
		_ = ioutil.WriteFile("/dev/termination-log", []byte(exitMsg), 0644)

		panic(exitMsg)
	}
}

func getStringSha1(name string) string {
	h := sha1.New()
	h.Write([]byte(name))
	hash := hex.EncodeToString(h.Sum(nil))
	return hash
}

func GetFSNameFromRequest(req *csi.CreateVolumeRequest) string {
	var filesystemName string
	filesystemName = req.GetParameters()["filesystemName"]
	if filesystemName == "" {
		filesystemName = defaultFilesystemName
	}
	return filesystemName
}

func GetFSName(volumeID string) string {
	// VolID format:
	// "dir/v1/<WEKA_FS_NAME>/<FOLDER_NAME_SHA1_HASH>-<FOLDER_NAME_ASCII>"
	slices := strings.Split(volumeID, "/")
	if len(slices) < 3 {
		return ""
	}
	return slices[2]
}

func GetVolumeDirName(volumeID string) string {
	slices := strings.Split(volumeID, "/")
	if len(slices) < 3 {
		return ""
	}
	return strings.Join(slices[3:], "/") // may be either directory name or innerPath
}

func GetVolumeFullPath(mountPoint, volumeID string) string {
	return filepath.Join(mountPoint, GetVolumeDirName(volumeID))
}

func GetVolumeType(volumeID string) string {
	slices := strings.Split(volumeID, "/")
	if len(slices) >= 2 {
		return strings.Join(slices[0:2], "/")
	}
	return ""
}

func TruncateString(s string, i int) string {
	runes := []rune(s)
	if len(runes) > i {
		return string(runes[:i])
	}
	return s
}

func getVolumeSize(path string) int64 {
	if capacityString, err := xattr.Get(path, xattrCapacity); err == nil {
		if capacity, err := strconv.ParseInt(string(capacityString), 10, 64); err == nil {
			return capacity
		}
		return 0
	}
	return 0 //TODO: Reconsider, it should return error, as we always supposed to set it
}

func PathExists(p string) bool {
	file, err := os.Open(p)
	if err != nil {
		return false
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return false
	}

	if !fi.IsDir() {
		exitMsg := "A file was found instead of directory in mount point"
		_ = ioutil.WriteFile("/dev/termination-log", []byte(exitMsg), 0644)
		panic(exitMsg)
	}
	return true
}

func PathIsWekaMount(path string) bool {
	glog.V(2).Infof("Checking if %s is wekafs mount", path)
	mountcmd := "mount -t wekafs | grep " + path
	res, _ := exec.Command("sh", "-c", mountcmd).Output()
	return strings.Contains(string(res), path)
}

func validatedVolume(mountPath string, mountErr error, volume dirVolume) (string, error) {
	glog.Infof("Validating if volume %s exists in %s", volume.id, mountPath)
	if mountErr != nil {
		return "", status.Error(codes.Internal, mountErr.Error())
	}
	volumePath := volume.getFullPath(mountPath)
	if _, err := os.Stat(volumePath); err != nil {
		if os.IsNotExist(err) {
			return "", status.Error(codes.NotFound, err.Error())
		} else {
			return "", status.Error(codes.Internal, err.Error())
		}
	}
	if err := pathIsDirectory(mountPath); err != nil {
		return "", status.Error(codes.Internal, err.Error())
	}
	glog.Infof("Volume %s exists and accessible via %s", volume.id, volumePath)
	return volumePath, nil
}

func validateVolumeId(volumeId string) error {
	if len(volumeId) == 0 {
		return status.Errorf(codes.InvalidArgument, "volume ID may not be empty")
	}
	if len(volumeId) > maxVolumeIdLength {
		return status.Errorf(codes.InvalidArgument, "volume ID exceeds max length")
	}

	volumeType := GetVolumeType(volumeId)
	switch volumeType {
	case VolumeTypeDirV1:
		// VolID format is as following:
		// "<VolType>/<WEKA_FS_NAME>/<FOLDER_NAME_SHA1_HASH>-<FOLDER_NAME_ASCII>"
		// e.g.
		// "dir/v1/default/63008f52b44ca664dfac8a64f0c17a28e1754213-my-awesome-folder"
		// length limited to maxVolumeIdLength
		r := VolumeTypeDirV1 + "/[^/]*/.+"
		re := regexp.MustCompile(r)
		if re.MatchString(volumeId) {
			return nil
		}
	}
	return status.Errorf(codes.InvalidArgument, "unsupported volumeID")
}

func updateXattrs(volPath string, attrs map[string][]byte) error {
	for key, val := range attrs {
		if err := xattr.Set(volPath, key, val); err != nil {
			return status.Errorf(codes.Internal, "failed to update volume attribute %s: %s", key, val)
		}
	}
	glog.V(3).Infof("Xattrs updated on volume: %v", volPath)
	return nil
}

func setVolumeProperties(volPath string, capacity int64, volName string) error {
	// assumes that volPath is already mounted and accessible
	xattrs := make(map[string][]byte)
	if volName != "" {
		xattrs[xattrVolumeName] = []byte(volName)
	}
	if capacity > 0 {
		xattrs[xattrCapacity] = []byte(fmt.Sprint(capacity))
	}
	return updateXattrs(volPath, xattrs)
}

func pathIsDirectory(filename string) error {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return status.Errorf(codes.NotFound, "volume path %s not found", filename)
	}
	if !info.IsDir() {
		return status.Errorf(codes.Internal, "volume path %s is not a valid directory", filename)
	}
	return nil
}
