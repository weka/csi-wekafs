package wekafs

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"golang.org/x/exp/constraints"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamp "google.golang.org/protobuf/types/known/timestamppb"
	"hash/fnv"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var ProcMountsPath = "/proc/mounts"

func generateInnerPathForDirBasedVol(dynamicVolPath, csiVolName string) string {
	requestedNameHash := getStringSha1(csiVolName)
	asciiPart := getAsciiPart(csiVolName, 64)
	folderName := asciiPart + "-" + requestedNameHash
	innerPath := "/" + folderName
	if dynamicVolPath != "" {
		innerPath = filepath.Join(dynamicVolPath, folderName)
	}
	return innerPath
}

// generateWekaObjectNameBase used for calculating of partial names for multiple Weka API objects
// will not be used directly in the code, only in the functions below
func generateWekaObjectNameBase(csiObjName string) string {
	truncatedName := getAsciiPart(csiObjName, MaxHashLengthForObjectNames)
	return truncatedName + "-" + getStringSha1AsB32(csiObjName)[:MaxHashLengthForObjectNames]
}

// generateWekaFsNameForFsBasedVol for every FS-based volume we create
// calculated from CSI volume name and will be used to construct the CSI volumeId
func generateWekaFsNameForFsBasedVol(prefix, csiVolName string) string {
	return prefix + generateWekaObjectNameBase(csiVolName)
}

// generateWekaSnapNameForSnapBasedVol derives the weka snapshot name for every Weka writable-snap-based volume
// calculated from CSI volume name and will be used to construct the CSI volumeId
func generateWekaSnapNameForSnapBasedVol(prefix, csiVolName string) string {
	return prefix + generateWekaObjectNameBase(csiVolName)
}

// generateWekaSnapAccessPointForSnapBasedVol derives the weka snapshot access point for every Weka writable-snap-based volume
// calculated from CSI volume name and will be used to construct the CSI volumeId
func generateWekaSnapAccessPointForSnapBasedVol(csiVolName string) string {
	return generateWekaObjectNameBase(csiVolName)
}

// generateVolumeIdFromComponents constructs a full-fledged volume ID from different components of the Volume
func generateVolumeIdFromComponents(volumeType VolumeType, filesystemName, snapshotAccessPoint, innerPath string) string {
	volId := string(volumeType) + "/" + filesystemName
	if snapshotAccessPoint != "" {
		volId += ":" + snapshotAccessPoint
	}
	if innerPath != "" {
		volId += "/" + innerPath
	}
	return volId
}

// generateWekaSnapNameForSnapshot used for creating Weka snapshot name
func generateWekaSnapNameForSnapshot(prefix, csiSnapName string) string {
	return prefix + generateWekaObjectNameBase(csiSnapName)
}

func generateSnapshotNameHash(csiSnapName string) string {
	return generateWekaObjectNameBase(csiSnapName)
}

// generateSnapshotIntegrityID is used to create a unique identifier for snapshot that encodes also source vol ID
// when CSI snapshot is created:
// - Weka snapshot name will be based on snapshot name only (for fast lookup),
// - Weka access point for the snapshot will be set with this integrity ID, that consists of hash of both of them
func generateSnapshotIntegrityID(name string, sourceVolumeId string) string {
	return getStringSha1AsB32(name + ":" + sourceVolumeId)[:MaxHashLengthForObjectNames]
}

// generateSnapshotIdFromComponents constructs a full-fledged snapshot ID from different components of the snapshot
func generateSnapshotIdFromComponents(snapshotType, filesystemName, snapshotNameHash, snapshotIntegrityId, innerPath string) string {
	volId := snapshotType + "/" + filesystemName + ":" + snapshotNameHash + ":" + snapshotIntegrityId
	if innerPath != "" {
		if !strings.HasPrefix(innerPath, "/") {
			innerPath = "/" + innerPath
		}
		volId += innerPath
	}
	return volId
}

// generateWekaSeedSnapshotName: for every new FS we create, we will create an empty seed snapshot right away,
// that would allow creating empty Snap volume based on that filesystem.
func generateWekaSeedSnapshotName(prefix, fsName string) string {
	return prefix + generateWekaSeedAccessPoint(fsName)
}

// generateWekaSeedAccessPoint: for every new FS we create, we will create an empty seed snapshot right away,
// that would allow creating empty Snap volume based on that filesystem.
func generateWekaSeedAccessPoint(fsName string) string {
	return getStringSha1AsB32(fsName)[:MaxHashLengthForObjectNames]
}

func getStringSha1(name string) string {
	h := sha1.New()
	h.Write([]byte(name))
	hash := hex.EncodeToString(h.Sum(nil))
	return hash
}

func getStringSha1AsB32(name string) string {
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

func sliceVolumeTypeFromVolumeId(volumeId string) VolumeType {
	slices := strings.Split(volumeId, "/")
	if len(slices) >= 2 {
		volTypeCandidate := strings.Join(slices[0:2], "/")
		for _, volType := range KnownVolTypes {
			if VolumeType(volTypeCandidate) == volType {
				return volType
			}
		}
	}
	if len(slices) > 1 && !strings.HasPrefix(slices[1], "v") {
		return VolumeTypeUNKNOWN // probably not in format of 'type/version'
	}
	if len(slices) == 1 {
		return VolumeTypeUNKNOWN
	}
	if slices[0] == "" {
		return VolumeTypeUNKNOWN // probably in format of Unix root path '/var/log/messages'
	}
	return VolumeTypeUnified
}

// sliceFilesystemNameFromVolumeId: given existing volumeId, slice the filesystem name part
func sliceFilesystemNameFromVolumeId(volumeId string) string {
	// VolID format:
	// "dir/v1/<WEKA_FS_NAME>[:<WEKA_SNAP_NAME>]/<FOLDER_NAME_SHA1_HASH>-<FOLDER_NAME_ASCII>"
	slices := strings.Split(volumeId, "/")
	if len(slices) < 3 {
		return ""
	}
	return strings.Split(slices[2], ":")[0]
}

// sliceSnapshotIntegrityIdFromSnapshotId: given existing snapshotId, slice the filesystem name part
func sliceSnapshotAccessPointFromVolumeId(volumeId string) string {
	// VolID format:
	// "dir/v1/<WEKA_FS_NAME>[:<WEKA_SNAP_NAME>]/<FOLDER_NAME_SHA1_HASH>-<FOLDER_NAME_ASCII>"
	slices := strings.Split(volumeId, "/")
	if len(slices) < 3 {
		return ""
	}
	slices = strings.Split(slices[2], ":")
	if len(slices) < 2 {
		return ""
	}
	return slices[1]
}

// sliceInnerPathFromVolumeId: returns innerPath from volumeId
func sliceInnerPathFromVolumeId(volumeId string) string {
	// VolID format:
	// "dir/v1/<WEKA_FS_NAME>[:<WEKA_SNAP_NAME>]/<FOLDER_NAME_SHA1_HASH>-<FOLDER_NAME_ASCII>"
	slices := strings.Split(volumeId, "/")
	if len(slices) <= 3 {
		return ""
	}
	return "/" + strings.Join(slices[3:], "/")
}

// sliceFilesystemNameFromSnapshotId: given existing snapshotID, slice the filesystem name part
func sliceFilesystemNameFromSnapshotId(snapshotId string) string {
	// SnapshotID format:
	// "wekasnap/v1/<WEKA_FS_NAME>:<SNAP_NAME_HASH>:<SNAP_INTEGRITY_ID>[/<INNER_PATH>]"
	slices := strings.Split(snapshotId, "/")
	if len(slices) < 3 {
		return ""
	}
	return strings.Split(slices[2], ":")[0]
}

// sliceSnapshotNameHashFromSnapshotId: returns base name from snapshot ID
// this name can be expanded to full Weka snapshot name by adding prefix
func sliceSnapshotNameHashFromSnapshotId(snapshotId string) string {
	// SnapshotID format:
	// "wekasnap/v1/<WEKA_FS_NAME>:<SNAP_NAME_HASH>:<SNAP_INTEGRITY_ID>[/<INNER_PATH>]"
	slices := strings.Split(snapshotId, "/")
	if len(slices) < 3 {
		return ""
	}
	slices = strings.Split(slices[2], ":")
	if len(slices) < 3 {
		return ""
	}
	return slices[1]
}

// sliceSnapshotIntegrityIdFromSnapshotId: returns the integrity id of CSI snapshot, which is also the AccessPoint name
func sliceSnapshotIntegrityIdFromSnapshotId(snapshotId string) string {
	// SnapshotID format:
	// "wekasnap/v1/<WEKA_FS_NAME>:<SNAP_NAME_HASH>:<SNAP_INTEGRITY_ID>[/<INNER_PATH>]"
	slices := strings.Split(snapshotId, "/")
	if len(slices) < 3 {
		return ""
	}
	slices = strings.Split(slices[2], ":")
	if len(slices) < 3 {
		return ""
	}
	return slices[2]
}

// sliceInnerPathFromSnapshotId: returns innerPath from snapshotId
func sliceInnerPathFromSnapshotId(snapshotId string) string {
	// SnapshotID format:
	// "wekasnap/v1/<WEKA_FS_NAME>:<SNAP_NAME_HASH>:<SNAP_INTEGRITY_ID>[/<INNER_PATH>]"
	slices := strings.Split(snapshotId, "/")
	if len(slices) <= 3 {
		return ""
	}
	return "/" + strings.Join(slices[3:], "/")
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

func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	return false
}

//goland:noinspection GoUnusedParameter
func PathIsWekaMount(ctx context.Context, path string) bool {
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && fields[2] == "wekafs" && fields[1] == path {
			return true
		}
		// TODO: better protect against false positives
		if len(fields) >= 3 && strings.HasPrefix(fields[2], "nfs") && fields[1] == path {
			return true
		}
	}

	return false
}

func GetMountIpFromActualMountPoint(mountPointBase string) (string, error) {
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return "", errors.New("failed to open /proc/mounts")
	}
	defer func() { _ = file.Close() }()
	var actualMountPoint string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && strings.HasPrefix(fields[1], fmt.Sprintf("%s-", mountPointBase)) {
			actualMountPoint = fields[1]
			return strings.TrimLeft(actualMountPoint, string(mountPointBase+"-")), nil
		}
	}
	return "", errors.New("mount point not found")
}
func GetMountContainerNameFromActualMountPoint(mountPointBase string) (string, error) {
	file, err := os.Open(ProcMountsPath)
	if err != nil {
		return "", errors.New("failed to open /proc/mounts")
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 4 && fields[2] == "wekafs" && strings.HasPrefix(fields[1], mountPointBase) {
			optionsString := fields[3]
			mountOptions := NewMountOptionsFromString(optionsString)
			containerName := mountOptions.getOptionValue("container_name")
			return containerName, nil
		}
	}
	return "", errors.New(fmt.Sprintf("mount point not found: %s", mountPointBase))
}

func validateVolumeId(volumeId string) error {
	// Volume New format:
	// VolID format is as following:
	// <VolType><WEKA_FS_NAME>[:<WEKA_SNAP_ACCESS_POINT>][/<INNER_PATH_ASCII>-<INNER_PATH_ASCII_SHA1_HASH>]
	// \__pfx__/\__ fsName __/\____ snapIdentifier _____/\___________________ innerPath __________________/
	// pfx is either dir/v1 or weka/v2

	if len(volumeId) == 0 {
		return status.Errorf(codes.InvalidArgument, "volume ID may not be empty")
	}
	if len(volumeId) > maxVolumeIdLength {
		return status.Errorf(codes.InvalidArgument, "volume ID exceeds max length")
	}

	volumeType := sliceVolumeTypeFromVolumeId(volumeId)
	switch volumeType {
	case VolumeTypeDirV1:
		// Old volume match
		// VolID format is as following:
		// "<VolType>/<WEKA_FS_NAME>/<FOLDER_NAME_ASCII>-<FOLDER_NAME_SHA1_HASH>"
		//  dir/v1/csi-filesystem/csi-volumes/my-test-volume-97ab4a2a2b6d7db8dce4ddd31723dc38d49b14b5
		//  \_pfx_/\__ fsName __/\_______________________________ innerPath ________________________/
		// snapIdentifier empty

		r := VolumeTypeDirV1 + "/[^/]*/.+"
		re := regexp.MustCompile(string(r))
		if re.MatchString(volumeId) {
			return nil
		}
	case VolumeTypeUnified:
		// New volume that is FS only
		// weka/v2/csivol-my-test-volu-97ab4a2a2b6d
		// \_pfx__/\______ fsName 32 chars _______/\SnapIdentifier/\InnerPath/
		// snapIdentifier, InnerPath empty

		//New volume that is snapshot on filesystem
		// weka/v2/csi-volsgen2:my-test-volu-97ab4a2a2b6d
		// \_pfx__/\_ fsName _/\____ SnapIdentifier ____/\InnerPath/
		// InnerPath empty
		// snapIdentifier - first chars of name + hash
		// Weka snap name: csivol-$snapIdentifier
		// Weka snap acess point $snapIdentifier

		//New volume that is a directory on snapshot
		// weka/v2/csi-volsgen2:my-test-volu-97ab4a2a2b6d/csi-volumes/my-test-volume-97ab4a2a2b6d7db8dce4ddd31723dc38d49b14b5
		// \_pfx__/\_ fsName _/\____ SnapIdentifier ____/\__________________________ innerPath _____________________________/
		// SnapIdentifier - first chars of name + hash
		// Weka snap name: csivol-$snapIdentifier
		// Weka snap acess point $snapIdentifier

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
	return status.Errorf(codes.InvalidArgument, fmt.Sprintf("unsupported volumeId %s for type %s", volumeId, volumeType))
}

func validateSnapshotId(snapshotId string) error {
	// SnapshotID format is as following:
	// <VolType><WEKA_FS_NAME>:<CSI_SNAP_NAME_HASH>:<SNAP_NAME+SRC_VOL_ID_HASH>[/<INNER_PATH_ASCII>-<INNER_PATH_ASCII_SHA1_HASH>]
	// \__pfx__/\__ fsName __/\___ snapNameHash ___/\____ snapIntegrityId _____/\___________________ innerPath __________________/
	//                        \_______________ SnapIdentifier _________________/
	// pfx: wekasnap/v1
	// snapNameHash is hash of CSI snapshot name
	// snapIntegrityId: additional hash of CSI sourceVolumeID/sourceSnapshotID + CSI snap name
	// Weka snapName is "csisnap-$snapNameHash"
	// accessPoint == snapIntegrityID

	// Snapshot of Directory volume:
	// wekasnap/v1/csi-volsgen2:my-first-sn-GQ4TCMRQMNTD:BWMQ3GMYTGGM/csi-volumes/my-test-volume-97ab4a2a2b6d7db8dce4ddd31723dc38d49b14b5
	// \__ pfx ___/\_ fsName _/\__________ SnapIdentifier __________/\____________________________ InnerPath ___________________________/
	//                         \_____ snapNameHash ____/\IntegrityId/

	// Snapshot of FS / Snapshot root volume:
	// wekasnap/v1/csi-volsgen2:my-first-sn-GQ4TCMRQMNTD:BWMQ3GMYTGGM
	// \__ pfx ___/\_ fsName _/\__________ SnapIdentifier __________/
	//                         \_____ snapNameHash ____/\IntegrityId/
	if len(snapshotId) == 0 {
		return status.Errorf(codes.InvalidArgument, "snapshot ID may not be empty")
	}
	if len(snapshotId) > maxVolumeIdLength {
		return status.Errorf(codes.InvalidArgument, "snapshot ID exceeds max length")
	}

	// "wekasnap/v1/my-filesystem:my-awsome-sn-GUZTQNZTMZSD:GQ4TCMRQMNTD/csi-volumes/asefgdfg-3f786850e387550fdab836ed7e6dc881de23001b"  FS name, snap name and internal path
	// "wekasnap/v1/my-filesystem:my-awsome-sn-GUZTQNZTMZSD:GQ4TCMRQMNTD # FS / FS Snap only (without internal path)

	// length limited to maxVolumeIdLength
	r := "[^:]+:[^:]+:[^/]+(/.+)*"
	if strings.HasPrefix(snapshotId, SnapshotTypeUnifiedSnap) {
		r = SnapshotTypeUnifiedSnap + r
	}
	re := regexp.MustCompile(r)
	if re.MatchString(snapshotId) {
		return nil
	} else {
		return errors.New(fmt.Sprintln("Snapshot ID does not match regex:", r, snapshotId))
	}
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

func Max[T constraints.Ordered](a, b T) T {
	if a > b {
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
		return false, errors.New("unsupported capacityEnforcement in volume params")
	}
	return enforceCapacity, nil
}

func volumeExistsAndMatchesCapacity(ctx context.Context, v *Volume, capacity int64) (bool, bool, error) {
	exists, err := v.Exists(ctx)
	if err != nil || !exists {
		return exists, false, err
	}
	reportedCapacity, err := v.GetCapacity(ctx)
	if err != nil {
		return true, false, err
	}
	matches := reportedCapacity == capacity
	return exists, matches, err
}

func isWekaDriverLoaded() bool {
	// check if the WEKA kernel driver is loaded
	file, err := os.Open(ProcModulesPath)
	if err != nil {
		log.Err(err).Msg("Failed to open procfs and check for existence of Weka kernel module")
		return false
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		s := strings.Split(scanner.Text(), " ")
		name := s[0]
		if name == WekaKernelModuleName {
			return true
		}
	}
	return false
}

func isWekaRunning() bool {
	if isWekaDriverLoaded() {
		// check if the WEKA client software is running (on top of the kernel module)
		driverInfo, err := os.Open(ProcWekafsInterface)
		if err != nil {
			log.Err(err).Msg("Failed to open driver interface and check for existence of Weka client software")
			return false
		}
		defer func() { _ = driverInfo.Close() }()
		scanner := bufio.NewScanner(driverInfo)
		for scanner.Scan() {
			line := scanner.Text()
			// TODO: improve it by checking for the explicit client container name rather than just ANY container
			if strings.Contains(line, "Connected frontend pid") {
				return true
			}
		}
		log.Error().Msg("Client software is not running on host and NFS is not enabled")
	}
	return false
}

func getSelinuxStatus(ctx context.Context) bool {
	logger := log.Ctx(ctx)
	data, err := os.ReadFile("/sys/fs/selinux/enforce")
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		logger.Error().Err(err).Msg("Error reading SELinux status")
		return false
	}

	mode := strings.TrimSpace(string(data))
	switch mode {
	case "1":
		return true
	case "0":
		return true
	default:
		return false
	}
}

func getDataTransportFromMountPath(mountPoint string) DataTransport {
	if strings.HasPrefix(mountPoint, path.Join(MountBasePath, string(dataTransportNfs))) {
		return dataTransportWekafs
	}
	if strings.HasPrefix(mountPoint, path.Join(MountBasePath, string(dataTransportWekafs))) {
		return dataTransportWekafs
	}
	// just default
	return dataTransportWekafs
}

// Die used to intentionally panic and exit, while updating termination log
func Die(exitMsg string) {
	_ = os.WriteFile("/dev/termination-log", []byte(exitMsg), 0644)
	panic(exitMsg)
}

func GetCsiPluginMode(mode *string) CsiPluginMode {
	ret := CsiPluginMode(*mode)
	switch ret {
	case CsiModeNode,
		CsiModeController,
		CsiModeAll,
		CsiModeMetricsServer:
		return ret
	default:
		log.Fatal().Str("required_plugin_mode", string(ret)).Msg("Unsupported plugin mode")
		return ""
	}
}

// hashString is a simple hash function that takes a string and returns a hash value in the range [0, n)
func hashString(s string, n int) int {
	if n == 0 {
		return 0
	}

	// Create a new FNV-1a hash
	h := fnv.New32a()

	// Write the string to the hash
	_, _ = h.Write([]byte(s))

	// Get the hash sum as a uint32
	hashValue := h.Sum32()

	// Return the hash value in the range of [0, n)
	return int(hashValue % uint32(n))
}

func (api *ApiStore) getLockForHash(hash uint32) *sync.Mutex {
	lockIface, _ := api.locks.LoadOrStore(hash, &sync.Mutex{})
	return lockIface.(*sync.Mutex)
}

func getOwnNamespace() (string, error) {
	file := "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	// Check if the file exists
	if _, err := os.Stat(file); err != nil {
		if os.IsNotExist(err) {
			// If the file does not exist, return a default namespace
			if os.Getenv("LEADER_ELECTION_NAMESPACE") != "" {
				return os.Getenv("LEADER_ELECTION_NAMESPACE"), nil
			}
		}
	}
	// read namespace from file
	data, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("failed to read namespace from %s: %w", file, err)
	}
	namespace := strings.TrimSpace(string(data))
	if namespace != "" {
		return namespace, nil
	}

	return "", errors.New("namespace not found or not set in environment variable LEADER_ELECTION_NAMESPACE")
	// Get the namespace from the environment variable
}
