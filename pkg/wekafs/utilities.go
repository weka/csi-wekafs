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
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func GetVolumeIdFromRequest(req *csi.CreateVolumeRequest) (string, error) {
	name := req.GetName()

	var volId string
	volType := req.GetParameters()["volumeType"]

	switch volType {
	case VolumeTypeDirV1:
		// we have a dirquota in request or no info
		filesystemName := GetFSNameFromRequest(req)
		asciiPart := getAsciiPart(name, 64)
		hash := getStringSha1(name)
		folderName := hash + "-" + asciiPart
		volId = volType + "/" + filesystemName + "/" + folderName
		return TruncateString(volId, maxVolumeIdLength), nil
	case "":
		return "", status.Errorf(codes.InvalidArgument, "missing VolumeType in CreateVolumeRequest")

	default:
		panic("Unsupported CreateVolumeRequest")
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
	return slices[len(slices)-1] // last part is a folder name
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

func getVolumeName(path string) string {
	// since we strip the non-ASCII characters and may also truncate the name - can't rely on that.
	// Instead, we will take this from Xattrs
	if volName, err := xattr.Get(path, xattrVolumeName); err == nil {
		return string(volName)
	}
	return "" //TODO: Reconsider, it should return error, as we always supposed to set it
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
		panic("A file was found instead of directory in mount point")
	}
	return true
}

func validatedVolume(mountPath string, mountErr error, volumeId string) (string, error) {
	if mountErr != nil {
		return "", status.Error(codes.Internal, mountErr.Error())
	}
	volumePath := filepath.Join(mountPath, GetVolumeDirName(volumeId))
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
	return volumePath, nil
}

func validateVolumeId(volumeId string) error {
	volumeType := GetVolumeType(volumeId)
	switch volumeType {
	case VolumeTypeDirV1:
		// VolID format is as following:
		// "<VolType>/<WEKA_FS_NAME>/<FOLDER_NAME_SHA1_HASH>-<FOLDER_NAME_ASCII>"
		// e.g.
		// "dir/v1/default/63008f52b44ca664dfac8a64f0c17a28e1754213-my-awesome-folder"
		// length limited to maxVolumeIdLength
		//slices := strings.Split(volumeId, "/")[2:]
		if len(volumeId) == 0 && len(volumeId) > maxVolumeIdLength {
			return status.Errorf(codes.InvalidArgument, "volume ID may not be empty")
		}
		// TODO: Reuse ascii ranges directly
		// TODO: validate dirName part against ascii filter
		r := VolumeTypeDirV1 + "/" + "[^/]*/" + "[0-9a-f]{40}" + "-" + "[A-Za-z0-9_.:-]+" + "$"
		re := regexp.MustCompile(r)
		if !re.MatchString(volumeId) {
			return status.Errorf(codes.InvalidArgument, "invalid volume ID specified")
		}
	default:
		return status.Errorf(codes.InvalidArgument, "unsupported not ID specified")
	}
	return nil
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
