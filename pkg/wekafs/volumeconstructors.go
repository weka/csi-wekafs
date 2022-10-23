package wekafs

import (
	"context"
	"errors"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"path/filepath"
	"strings"
)

func NewVolumeFromId(ctx context.Context, volumeId string, apiClient *apiclient.ApiClient, mounter *wekaMounter) (Volume, error) {
	glog.V(5).Infof("Initializing representation object for volume ID %s", volumeId)
	if err := validateVolumeId(volumeId); err != nil {
		glog.Errorln("Failed to validate volume ID", volumeId)
		return &UnifiedVolume{}, err
	}
	if apiClient != nil {
		glog.V(5).Infof("Successfully bound volume to backend API client %s", apiClient.Credentials.String())
	} else {
		glog.V(5).Infof("Volume was not bound to any backend API client")
	}
	volumeType := GetVolumeType(volumeId)
	volId := ""
	switch volumeType {
	case VolumeTypeDirV1:
		volId = string(VolumeTypeUnified) + strings.TrimPrefix(volumeId, string(VolumeTypeDirV1))
	case VolumeTypeFsV1:
		volId = string(VolumeTypeUnified) + strings.TrimPrefix(volumeId, string(VolumeTypeFsV1))
	case VolumeTypeNone:
		volId = string(VolumeTypeUnified) + "/" + volumeId
	case VolumeTypeUnified:
		// assume we always have a unified volume unless specified otherwise
		volId = volumeId
		if !strings.HasPrefix(volumeId, string(VolumeTypeUnified)) {
			volId = string(VolumeTypeUnified) + "/" + volumeId
		}
	default:
		return nil, errors.New("unsupported volume type requested")
	}
	v := &UnifiedVolume{
		id:             volId,
		FilesystemName: GetFSName(volId),
		SnapshotUuid:   GetSnapshotUuid(volId),
		innerPath:      GetVolumeDirName(volId),
		apiClient:      apiClient,
		permissions:    DefaultVolumePermissions,
		mounter:        mounter,
		mountPath:      make(map[bool]string),
	}
	glog.Infoln("Successfully initialized object", v.String())
	return v, nil
}

func NewVolumeFromControllerCreateRequest(ctx context.Context, req *csi.CreateVolumeRequest, cs *ControllerServer) (Volume, error) {
	// obtain client for volume.
	// client can be also nil if no API secrets bound for request
	// Need to calculate volumeID first thing due to possible mapping to multiple FSes

	// Check if volume should be created from source
	var volume Volume
	var err error
	var cSourceVolume *csi.VolumeContentSource_VolumeSource
	var cSourceSnapshot *csi.VolumeContentSource_SnapshotSource
	cSource := req.GetVolumeContentSource()
	if cSource != nil {
		cSourceVolume = cSource.GetVolume()
		cSourceSnapshot = cSource.GetSnapshot()
	}

	if cSourceSnapshot != nil {
		// this is volume from source snapshot (CREATE_FROM_SNAPSHOT)
		volume, err = NewVolumeForSrcSnapshotVolumeRequest(ctx, req, cs)
		if err != nil {
			return nil, err
		}
	}

	if cSourceVolume != nil {
		// this is volume from source volume (CLONE_VOLUME)
		volume, err = NewVolumeForSrcVolumeVolumeRequest(ctx, req, cs.dynamicVolPath, cs)
		if err != nil {
			return nil, err
		}
	}

	// this is blank volume
	volume, err = NewVolumeForBlankVolumeRequest(ctx, req, cs.dynamicVolPath, cs)
	if err != nil {
		return nil, err
	}
	params := req.GetParameters()
	glog.Infoln("Received the following request params:", renderKeyValuePairs(params))
	err = volume.SetParamsFromRequestParams(params)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not set parameters on volume")
	}

	return volume, nil
}

// NewVolumeForBlankVolumeRequest can create a new volume of those types: new raw FS, snapshot of empty FS, directory on predefined filesystem
func NewVolumeForBlankVolumeRequest(ctx context.Context, req *csi.CreateVolumeRequest, dynamicVolPath string, cs *ControllerServer) (Volume, error) {
	// obtain API client (or no client for legacy)
	client, err := cs.api.GetClientFromSecrets(ctx, req.GetSecrets())
	if err != nil {
		return nil, err
	}

	requestedVolumeName := req.GetName()
	requestedNameHash := getStringSha1(requestedVolumeName)
	volType := VolumeType(req.GetParameters()["volumeType"])

	var volId string
	var vol Volume

	filesystemName := GetFSNameFromRequest(req)
	if filesystemName == "" {
		// filesystem name not specified, we assume this either is a new FS provisioned as a volume, OR error
		if volType == VolumeTypeDirV1 {
			// explicitly required to create DirVolume, hence FS name is mandatory: return an explicit error
			return nil, status.Errorf(codes.InvalidArgument, "missing filesystemName in CreateVolumeRequest")
		}

		if !cs.allowAutoFsCreation {
			// we are expected to create a new FS, but are not allowed to: return an explicit error
			return nil, status.Errorf(codes.PermissionDenied, "creating new filesystems is not allowed on CSI driver configuration")
		}

		// assume that this is a dynamical provision of a raw FS volume, must be allowed in configuration
		filesystemName = cs.newVolumePrefix + calculateFsBaseNameForUnifiedVolume(requestedVolumeName)
		volId = filepath.Join(string(VolumeTypeUnified), filesystemName)
		vol = &UnifiedVolume{
			id:             volId,
			FilesystemName: filesystemName,
			innerPath:      "",
			apiClient:      client,
			mounter:        cs.mounter,
			mountPath:      make(map[bool]string),
		}
	} else {

		// filesystem name is specified, we assume this is a new snapshot volume OR new dir provisioned as a volume, depends on volumeType in request
		if volType == VolumeTypeDirV1 {
			// explicitly required to create DirVolume
			asciiPart := getAsciiPart(requestedVolumeName, 64)
			folderName := asciiPart + "-" + requestedNameHash
			innerPath := folderName
			if dynamicVolPath != "" {
				volId = filepath.Join(string(volType), filesystemName, dynamicVolPath, folderName)
				innerPath = filepath.Join(dynamicVolPath, folderName)
			} else {
				volId = filepath.Join(string(volType), filesystemName, folderName)
			}

			vol = &UnifiedVolume{
				id:             volId,
				FilesystemName: filesystemName,
				innerPath:      innerPath,
				apiClient:      client,
				mounter:        cs.mounter,
				mountPath:      make(map[bool]string),
			}
		} else {
			// assume we create a new snapshot of a filesystem
			// TODO: need to validate that the filesystem is indeed empty an return error otherwise

			if !client.SupportsQuotaOnSnapshots() {
				return nil, status.Error(codes.FailedPrecondition, "Quota not supported for snapshots, please upgrade Weka cluster to latest version")
			}
			snapName := calculateSnapNameForSnapVolume(requestedVolumeName, filesystemName)
			snapAccessPoint := calculateSnapshotParamsHash(requestedVolumeName, filesystemName)
			vol = &UnifiedVolume{
				id:                  "",
				FilesystemName:      "",
				SnapshotName:        snapName,
				SnapshotAccessPoint: snapAccessPoint,
				apiClient:           client,
				mounter:             cs.mounter,
				mountPath:           make(map[bool]string),
			}

		}

	}
	glog.Infoln("Constructed a new volume representation", vol)
	return vol, nil
}

// NewVolumeForSrcSnapshotVolumeRequest can accept those possible combinations:
// - DirectorySnapshot (has innePath and source Weka snapshot)
// - FsSnapshot (has no innerPath and source Weka filesystem)
// - New volume will be always in new format, any volumeType set in StorageClass will be ignored
func NewVolumeForSrcSnapshotVolumeRequest(ctx context.Context, req *csi.CreateVolumeRequest, cs *ControllerServer) (Volume, error) {
	// obtain API client
	client, err := cs.api.GetClientFromSecrets(ctx, req.GetSecrets())
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, status.Errorf(codes.InvalidArgument, "cannot create volume without API binding")
	}

	requestedVolumeName := req.GetName()
	requestedNameHash := getStringSha1(requestedVolumeName)[:MaxHashLengthForObjectNames]

	sourceSnapId := req.GetVolumeContentSource().GetSnapshot().GetSnapshotId() // we can assume no nil pointer as the function is called only if it happens

	sourceSnap, err := NewSnapshotFromId(ctx, sourceSnapId, client)
	if err != nil {
		// although we failed to create snapshot from ID because it is invalid, still return NOT_EXISTS
		//return nil, status.Errorf(codes.Internal, "Could not initialize source content snapshot object from ID %s", sourceSnapId)
		return nil, status.Errorf(codes.NotFound, "Source snapshot %s does not exist, cannot create volume", sourceSnapId)

	}
	sourceSnapObj, err := sourceSnap.getObject(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to check for existence of source snapshot %s", sourceSnapId)
	}
	if sourceSnapObj == nil {
		return nil, status.Errorf(codes.NotFound, "Source snapshot %s does not exist, cannot create volume", sourceSnapId)
	}

	if sourceSnapObj.IsWritable {
		return nil, status.Errorf(codes.FailedPrecondition, "Source snapshot %s is writable, cannot create volume from writable snapshot", sourceSnapId)
	}

	// check integrity and make sure that source snapshot ID refers to same filesystem as in Weka cluster
	sourceFsName := GetFSName(sourceSnapId)
	if sourceFsName != sourceSnapObj.Filesystem {
		return nil, status.Errorf(codes.Internal, "Integrity check failure: source snapshot ID %s points on filesystem %s, while in Weka cluster FS name is %s",
			sourceSnapId, sourceFsName, sourceSnapObj.Filesystem)
	}

	targetSnapName := cs.newSnapshotPrefix + calculateSnapBaseNameForSnapshot(requestedVolumeName, sourceSnapId)
	targetAccessPoint := calculateSnapshotParamsHash(requestedVolumeName, sourceSnapId)
	volId := string(VolumeTypeUnified) + sourceFsName + ":" + targetAccessPoint + "/" + sourceSnap.getInnerPath() + ":" + requestedNameHash
	vol := &UnifiedVolume{
		id:                  volId,
		FilesystemName:      sourceSnapObj.Filesystem,
		filesystemGroupName: "",
		SnapshotName:        targetSnapName,
		SnapshotAccessPoint: targetAccessPoint,
		innerPath:           sourceSnap.getInnerPath(),
		apiClient:           client,
		mounter:             cs.mounter,
		mountPath:           make(map[bool]string),
		enforceCapacity:     true,
		srcSnapshotUid:      &(sourceSnapObj.Uid),
	}
	return vol, nil
}

func NewVolumeForSrcVolumeVolumeRequest(ctx context.Context, req *csi.CreateVolumeRequest, dynamicVolPath string, cs *ControllerServer) (Volume, error) {
	// obtain API client
	client, err := cs.api.GetClientFromSecrets(ctx, req.GetSecrets())
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, status.Errorf(codes.InvalidArgument, "cannot create volume without API binding")
	}

	requestedVolumeName := req.GetName()
	requestedNameHash := getStringSha1(requestedVolumeName)
	volType := VolumeType(req.GetParameters()["volumeType"])

	var volId string
	var vol UnifiedVolume

	filesystemName := GetFSNameFromRequest(req)
	if filesystemName == "" {
		// filesystem name not specified, we assume this either is a new FS provisioned as a volume, OR error
		if volType == VolumeTypeDirV1 {
			// for this case, FS name is mandatory, speaking of legacy volume, return an explicit error
			return nil, status.Errorf(codes.InvalidArgument, "missing filesystemName in CreateVolumeRequest")
		}

		if !cs.allowAutoFsCreation {
			// we could create a new FS, but are not allowed to, return an explicit error
			return nil, status.Errorf(codes.PermissionDenied, "creating new filesystems is not allowed on CSI driver configuration")
		}

		// assume that this is a dynamical provision of a raw FS volume, must be allowed in configuration
		filesystemName = calculateFsBaseNameForUnifiedVolume(requestedVolumeName)
		volId = filepath.Join(string(VolumeTypeUnified), filesystemName)
		vol = UnifiedVolume{
			id:                 volId,
			FilesystemName:     filesystemName,
			innerPath:          "",
			apiClient:          client,
			permissions:        0,
			ownerUid:           0,
			ownerGid:           0,
			mounter:            cs.mounter,
			mountPath:          make(map[bool]string),
			ssdCapacityPercent: 0,
		}
	}

	// filesystem name is specified, we assume this is a new snapshot OR new dir provisioned as a volume, depends on volumeType in request
	if volType == VolumeTypeDirV1 {
		asciiPart := getAsciiPart(requestedVolumeName, 64)
		folderName := asciiPart + "-" + requestedNameHash

		return &UnifiedVolume{
			id:                 volId,
			FilesystemName:     filesystemName,
			innerPath:          folderName,
			apiClient:          nil,
			permissions:        0,
			ownerUid:           0,
			ownerGid:           0,
			mounter:            cs.mounter,
			mountPath:          make(map[bool]string),
			ssdCapacityPercent: 0,
		}, nil
	}

	asciiPart := getAsciiPart(requestedVolumeName, 64)
	folderName := asciiPart + "-" + requestedNameHash
	if dynamicVolPath != "" {
		volId = filepath.Join(string(volType), filesystemName, dynamicVolPath, folderName)
	} else {
		volId = filepath.Join(string(volType), filesystemName, folderName)
	}
	return &vol, nil
}
