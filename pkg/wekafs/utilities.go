package wekafs

import (
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/pkg/xattr"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"golang.org/x/exp/constraints"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamp "google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"
)

func calculateSnapshotParamsHash(name string, sourceVolumeId string) string {
	return getStringSha1(name)[:MaxHashLengthForObjectNames] + getStringSha1(sourceVolumeId)[:MaxHashLengthForObjectNames]
}

func calculateFsBaseNameForUnifiedVolume(name string) string {
	truncatedName := getAsciiPart(name, MaxHashLengthForObjectNames)
	return truncatedName + "-" + getStringSha1AsB64(name)[:MaxHashLengthForObjectNames]
}

func calculateSnapNameForSnapVolume(name string, fsName string) string {
	truncatedVolName := getAsciiPart(name, 12)
	paramsHash := getStringSha1AsB64(fsName + ":" + name)[:MaxHashLengthForObjectNames]
	return truncatedVolName + "-" + paramsHash
}
func calculateSnapBaseNameForSnapshot(name string, sourceVolumeId string) string {
	paramsHash := getStringSha1AsB64(name)[:MaxHashLengthForObjectNames] + getStringSha1AsB64(sourceVolumeId)[:MaxHashLengthForObjectNames]
	return paramsHash
}

func getStringSha1(name string) string {
	h := sha1.New()
	h.Write([]byte(name))
	hash := hex.EncodeToString(h.Sum(nil))
	return hash
}

func getStringSha1AsB64(name string) string {
	h := sha1.New()
	h.Write([]byte(name))
	hash := base32.StdEncoding.EncodeToString(h.Sum(nil))
	return hash
}

func GetFSNameFromRequest(req *csi.CreateVolumeRequest) string {
	var filesystemName string
	if val, ok := req.GetParameters()["filesystemName"]; ok {
		// explicitly specified FS name in request
		filesystemName = val
		if filesystemName != "" {
			return filesystemName
		}
	}
	return ""
}

func GetFSName(volumeID string) string {
	// VolID format:
	// "dir/v1/<WEKA_FS_NAME>[:<WEKA_SNAP_NAME>]/<FOLDER_NAME_SHA1_HASH>-<FOLDER_NAME_ASCII>"
	slices := strings.Split(volumeID, "/")
	if len(slices) < 3 {
		return ""
	}
	return strings.Split(slices[2], ":")[0]
}

func GetVolumeDirName(volumeID string) string {
	slices := strings.Split(volumeID, "/")
	if len(slices) < 3 {
		return ""
	}
	return strings.Join(slices[3:], "/") // may be either directory name or innerPath
}

func GetSnapshotUuid(snapshotId string) *uuid.UUID {
	// VolID format:
	// "dir/v1/<WEKA_FS_NAME>[:<WEKA_SNAP_NAME>]/<FOLDER_NAME_SHA1_HASH>-<FOLDER_NAME_ASCII>"
	slices := strings.Split(snapshotId, "/")
	if len(slices) < 3 {
		return nil
	}
	spl := strings.Split(slices[2], ":")
	if len(spl) == 2 {
		if id, err := uuid.Parse(spl[len(spl)-1]); err != nil {
			return &id
		}
	}
	return nil
}

func GetSnapshotParamsHash(snapshotId string) string {
	spl := strings.Split(snapshotId, ":")
	if len(spl) < 3 {
		return ""
	}
	return spl[len(spl)-1]
}

func GetSnapshotInternalPath(snapshotId string) string {
	// getting snapshot Id of one of the following formats:
	// "aaaa:{SnapshotUuid}:HASH"   	# FS name, snap name (root of volume) + hash
	// "aaaa:{SnapshotUuid}/snapaaaa/ass/s-aifsda0fyd:HASH"   	# FS name, snap name and internal path + hash

	spl := strings.Split(snapshotId, ":")
	if len(spl) < 3 {
		// it is invalid string in our case
		return ""
	}
	pathParts := strings.Split(snapshotId, "/")

	if len(pathParts) < 2 {
		// for a case where inner path is a root of the FS
		// {SnapshotUuid}
		return ""
	} else {
		// inner path is between snapshot Uid and HASH
		return path.Join(pathParts[1 : len(pathParts)-1]...)
	}

}

func GetVolumeType(volumeID string) VolumeType {
	slices := strings.Split(volumeID, "/")
	if len(slices) >= 2 {
		volTypeCandidate := strings.Join(slices[0:2], "/")
		for _, volType := range KnownVolTypes {
			if VolumeType(volTypeCandidate) == volType {
				return volType
			}
		}
	}
	if slices[0] == "" {
		return VolumeTypeUNKNOWN // probably in format of Unix root path '/var/log/messages'
	}
	if len(slices) > 1 && !strings.HasPrefix(slices[1], "v") {
		return VolumeTypeUNKNOWN // probably not in format of 'type/version'
	}
	return VolumeTypeUnified
}

func PathExists(p string) bool {
	file, err := os.Open(p)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()

	fi, err := file.Stat()
	if err != nil {
		return false
	}

	if !fi.IsDir() {
		Die("A file was found instead of directory in mount point. Please contact support")
	}
	return true
}

func PathIsWekaMount(path string) bool {
	glog.V(2).Infof("Checking if %s is wekafs mount", path)
	mountcmd := "mount -t wekafs | grep " + path
	res, _ := exec.Command("sh", "-c", mountcmd).Output()
	return strings.Contains(string(res), path)
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
		// "<VolType>/<WEKA_FS_NAME>/<FOLDER_NAME_ASCII>-<FOLDER_NAME_SHA1_HASH>"
		// e.g.
		// "dir/v1/default/my-awesome-folder-HASH"
		// length limited to maxVolumeIdLength
		r := VolumeTypeDirV1 + "/[^/]*/.+"
		re := regexp.MustCompile(string(r))
		if re.MatchString(volumeId) {
			return nil
		}
	case VolumeTypeUnified:
		// VolID format is as following:
		// "[<WEKA_FS_NAME>][:<WEKA_SNAP_UID>][/<INNER_PATH_ASCII>-<INNER_PATH_ASCII_SHA1_HASH>][:NAME_HASH]"
		// e.g.
		// "aaaa:{SnapshotUuid}/snapaaaa-aifsda0fyd:xxxx # FS name, snap, inner path, name hash. Required for volumes that were created from snapshot or other volume
		// "aaaa:{SnapshotUuid}/snapaaaa-aifsda0fyd"   	# FS name, snap and directory (e.g. volume created from snapshot of directory)
		// "aaaa/snapaaaa-aifsda0fyd"          	# FS name, directory (as in legacy model)
		// "aaaa:snap01"	       			 	# FS name, snap (e.g. volume created from snapshot of fs)
		// "aaaa"								# FS name only (e.g. new volume provisioned on top of filesystem)

		// length limited to maxVolumeIdLength
		r := "[^:/]+(:[^/]+)*(/[^:]+)*(:[.+])*"
		if strings.HasPrefix(volumeId, string(VolumeTypeUnified)) {
			r = string(VolumeTypeUnified) + "/" + r
		}
		re := regexp.MustCompile(r)
		if re.MatchString(volumeId) {
			return nil
		} else {
			return errors.New(fmt.Sprintln("Volume ID does not match regex:", r, volumeId))
		}
	}
	return status.Errorf(codes.InvalidArgument, fmt.Sprintf("unsupported volumeID %s for type %s", volumeId, volumeType))
}

func validateSnapshotId(snapshotId string) error {
	if len(snapshotId) == 0 {
		return status.Errorf(codes.InvalidArgument, "snapshot ID may not be empty")
	}
	if len(snapshotId) > maxVolumeIdLength {
		return status.Errorf(codes.InvalidArgument, "snapshot ID exceeds max length")
	}

	// SnapshotId format can be one of the following:
	// "<WEKA_FS_NAME>:<WEKA_SNAP_UID>[/<FOLDER_NAME_ASCII>-<FOLDER_NAME_SHA1_HASH>]:<SNAP_NAME+SRC_VOL_ID_HASH>"
	// e.g.
	// "aaaa:{SnapshotUuid}/snapaaaa-aifsda0fyd:HASH"   	# FS name, snap name and internal path + hash
	// "aaaa:{SnapshotUuid}:HASH"   	# FS name, snap name (root of volume) + hash

	// length limited to maxVolumeIdLength
	r := "[^:]+:[a-z0-9-]{36}(/[^:]+)*:[A-Za-z0-9]+"
	if strings.HasPrefix(snapshotId, string(VolumeTypeUnifiedSnap)) {
		r = string(VolumeTypeUnifiedSnap) + r
	}
	re := regexp.MustCompile(r)
	if re.MatchString(snapshotId) {
		return nil
	} else {
		return errors.New(fmt.Sprintln("Snapshot ID does not match regex:", r, snapshotId))
	}
}

func updateXattrs(volPath string, attrs map[string][]byte) error {
	for key, val := range attrs {
		if err := xattr.Set(volPath, key, val); err != nil {
			return status.Errorf(codes.Internal, "failed to update volume attribute %s: %s, %s", key, val, err.Error())
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

func time2Timestamp(t time.Time) *timestamp.Timestamp {
	return &timestamp.Timestamp{
		Seconds: t.Unix(),
		Nanos:   int32(t.Nanosecond()),
	}
}

func Min[T constraints.Ordered](a, b T) T {
	if a < b {
		return a
	}
	return b
}

func getCapacityEnforcementParam(params map[string]string) (bool, error) {
	qt := ""
	if val, ok := params["capacityEnforcement"]; ok {
		qt = val
	}
	enforceCapacity := true
	switch apiclient.QuotaType(qt) {
	case apiclient.QuotaTypeSoft:
		enforceCapacity = false
	case apiclient.QuotaTypeHard:
		enforceCapacity = true
	case "":
		enforceCapacity = true
	default:
		glog.Warningf("Could not recognize capacity enforcement in params: %s", qt)
		return false, errors.New("unsupported capacityEnforcement in volume params")
	}
	return enforceCapacity, nil
}
