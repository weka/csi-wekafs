package wekafs

import (
	"context"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func NewVolumeFromId(ctx context.Context, volumeId string, apiClient *apiclient.ApiClient, server AnyServer) (Volume, error) {
	logger := log.Ctx(ctx).With().Str("volume_id", volumeId).Logger()
	logger.Trace().Msg("Initializating volume object")
	if err := validateVolumeId(volumeId); err != nil {
		logger.Debug().Err(err).Msg("Failed to validate volume ID")
		return &UnifiedVolume{}, err
	}
	if apiClient != nil {
		logger.Trace().Msg("Successfully bound volume to backend API client")
	}
	logger = log.Ctx(ctx).With().Str("volume_id", volumeId).Logger()

	v := &UnifiedVolume{
		id:                  volumeId,
		FilesystemName:      sliceFilesystemNameFromVolumeId(volumeId),
		SnapshotName:        sliceSnapshotNameFromVolumeId(server.getConfig().VolumePrefix, volumeId),
		SnapshotAccessPoint: sliceSnapshotAccessPointFromVolumeId(volumeId),
		innerPath:           sliceInnerPathFromVolumeId(volumeId),
		apiClient:           apiClient,
		permissions:         DefaultVolumePermissions,
		mountPath:           make(map[bool]string),
		server:              server,
	}
	v.initMountOptions(ctx)
	return v, nil
}

func sliceSnapshotNameFromVolumeId(prefix, volumeId string) string {
	base := sliceSnapshotAccessPointFromVolumeId(volumeId)
	if base != "" {
		return prefix + base
	}
	return ""
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
	logger := log.Ctx(ctx)
	cSource := req.GetVolumeContentSource()
	origin := "blank_volume"
	if cSource != nil {
		cSourceVolume = cSource.GetVolume()
		cSourceSnapshot = cSource.GetSnapshot()

		if cSourceSnapshot != nil {
			// this is volume from source snapshot (CREATE_FROM_SNAPSHOT)
			origin = "source_snapshot"
			volume, err = NewVolumeForCreateFromSnapshotRequest(ctx, req, cs)
			if err != nil {
				return nil, err
			}
		} else if cSourceVolume != nil {
			// this is volume from source volume (CLONE_VOLUME)
			origin = "source_volume"
			volume, err = NewVolumeForCloneVolumeRequest(ctx, req, cs)
			if err != nil {
				return nil, err
			}
		} else {
			logger.Warn().Msg("Received a request with content source but without definition")
			// this is blank volume
			volume, err = NewVolumeForBlankVolumeRequest(ctx, req, cs.getConfig().DynamicVolPath, cs)
			if err != nil {
				return nil, err
			}
		}

	} else {
		// this is blank volume
		volume, err = NewVolumeForBlankVolumeRequest(ctx, req, cs.getConfig().DynamicVolPath, cs)
		if err != nil {
			return nil, err
		}
	}
	volume.initMountOptions(ctx)
	params := req.GetParameters()
	err = volume.SetParamsFromRequestParams(ctx, params)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not set parameters on volume")
	}
	logger.Trace().Object("volume_info", volume).Str("origin", origin).Msg("Successfully initialized object")
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
	volType := VolumeType(req.GetParameters()["volumeType"])

	var innerPath string
	var snapName string
	var snapAccessPoint string
	mountPath := make(map[bool]string)

	filesystemName := GetFSNameFromRequest(req)

	if filesystemName == "" {
		// filesystem name not specified, we assume this either is a new FS provisioned as a volume, OR error
		if volType == VolumeTypeDirV1 {
			// explicitly required to create DirVolume, hence FS name is mandatory: return an explicit error
			return nil, status.Errorf(codes.InvalidArgument, "missing filesystemName in CreateVolumeRequest")
		} else if volType == VolumeTypeEmpty {
			volType = VolumeTypeUnified
		}
		if !cs.getConfig().allowAutoFsCreation {
			// assume that this is a dynamical provision of a raw FS volume, must be allowed in configuration
			return nil, status.Errorf(codes.PermissionDenied, "creating new filesystems is not allowed, check CSI driver configuration")
		}
		filesystemName = generateWekaFsNameForFsBasedVol(cs.getConfig().VolumePrefix, requestedVolumeName)
	} else {
		// filesystem name is specified, we assume this is a new snapshot volume OR new dir provisioned as a volume, depends on volumeType in request
		if volType == VolumeTypeDirV1 {
			// explicitly required to create DirVolume by setting volumeType=dir/v1 in StorageClass
			innerPath = generateInnerPathForDirBasedVol(dynamicVolPath, requestedVolumeName)
		} else {
			volType = VolumeTypeUnified

			if !client.SupportsQuotaOnSnapshots() && !cs.config.alwaysAllowSnapshotVolumes {
				return nil, status.Error(codes.FailedPrecondition, "Quota enforcement is not supported for snapshot-based volumes by current Weka software version, please upgrade Weka cluster")
			}
			snapName = generateWekaSnapNameForSnapBasedVol(cs.getConfig().VolumePrefix, requestedVolumeName)
			snapAccessPoint = generateWekaSnapAccessPointForSnapBasedVol(requestedVolumeName)
		}

	}
	volId := generateVolumeIdFromComponents(volType, filesystemName, snapAccessPoint, innerPath)
	volume := &UnifiedVolume{
		id:                  volId,
		FilesystemName:      filesystemName,
		SnapshotName:        snapName,
		SnapshotAccessPoint: snapAccessPoint,
		innerPath:           innerPath,
		apiClient:           client,
		mountPath:           mountPath,
		server:              cs,
	}
	return volume, nil
}

// NewVolumeForCreateFromSnapshotRequest can accept those possible combinations:
// - DirectorySnapshot (has innePath and source Weka snapshot)
// - FsSnapshot (has no innerPath and source Weka filesystem)
// - New volume will be always in new format, any volumeType set in StorageClass will be ignored
func NewVolumeForCreateFromSnapshotRequest(ctx context.Context, req *csi.CreateVolumeRequest, server AnyServer) (Volume, error) {
	// obtain API client
	client, err := server.(*ControllerServer).api.GetClientFromSecrets(ctx, req.GetSecrets())
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, status.Errorf(codes.InvalidArgument, "cannot create volume without API binding")
	}

	requestedVolumeName := req.GetName()

	sourceSnapId := req.GetVolumeContentSource().GetSnapshot().GetSnapshotId() // we can assume no nil pointer as the function is called only if it happens
	if sourceSnapId == "" {
		return nil, status.Error(codes.InvalidArgument, "Source snapshot ID is empty")
	}
	mountPath := make(map[bool]string)
	sourceSnap, err := NewSnapshotFromId(ctx, sourceSnapId, client, server)
	if err != nil {
		// although we failed to create snapshot from ID because it is invalid, still return NOT_EXISTS
		//return nil, status.Errorf(codes.Internal, "Could not initialize source content snapshot object from ID %s", sourceSnapId)
		return nil, status.Errorf(codes.NotFound, "Source snapshot %s does not exist, cannot create volume", sourceSnapId)

	}

	if sourceSnap.hasInnerPath() && !server.getConfig().allowSnapshotsOfLegacyVolumes {
		// block creation of snapshots from legacy volumes, as it wastes space
		return nil, status.Errorf(codes.FailedPrecondition, "Creation of snapshots is prohibited on directory-based CSI volumes. "+
			"Refer to Weka CSI plugin documentation")
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
	sourceFsName := sliceFilesystemNameFromVolumeId(sourceSnapId)
	if sourceFsName != sourceSnapObj.Filesystem {
		return nil, status.Errorf(codes.Internal, "Integrity check failure: source snapshot ID %s points on filesystem %s, while in Weka cluster FS name is %s",
			sourceSnapId, sourceFsName, sourceSnapObj.Filesystem)
	}

	// we assume the following when creating a new volume from existing snapshot:
	// - VolumeType can always be new, no upgrade issues should occur as those are new volumes
	// - fsName must remain as is
	// - innerPath must remain as is
	// - accessPoint must point on the new snapshot
	// Hence:
	// - sourceVolumeID is not participating in generation of names
	// - accessPoint must be calculated as usual, from volume name
	// - snapshot name must be calculated as as usual too

	targetWekaSnapName := generateWekaSnapNameForSnapBasedVol(server.getConfig().VolumePrefix, requestedVolumeName)
	targetWekaSnapAccessPoint := generateWekaSnapAccessPointForSnapBasedVol(requestedVolumeName)

	innerPath := sourceSnap.getInnerPath()
	volId := generateVolumeIdFromComponents(VolumeTypeUnified, sourceFsName, targetWekaSnapAccessPoint, innerPath)
	vol := &UnifiedVolume{
		id:                  volId,
		FilesystemName:      sourceSnapObj.Filesystem,
		filesystemGroupName: "",
		SnapshotName:        targetWekaSnapName,
		SnapshotAccessPoint: targetWekaSnapAccessPoint,
		innerPath:           innerPath,
		apiClient:           client,
		mountPath:           mountPath,
		enforceCapacity:     true,
		srcSnapshot:         sourceSnap,
		server:              server,
	}
	return vol, nil
}

// NewVolumeForCloneVolumeRequest can accept those possible combinations:
// - DirectoryVolume (has innePath but no Weka snapshot)
// - FSVolume (has no innerPath and no snapshot)
// - New volume will be always in new format, any volumeType set in StorageClass will be ignored
func NewVolumeForCloneVolumeRequest(ctx context.Context, req *csi.CreateVolumeRequest, server AnyServer) (Volume, error) {
	logger := log.Ctx(ctx)
	// obtain API client
	client, err := server.getApiStore().GetClientFromSecrets(ctx, req.GetSecrets())
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, status.Errorf(codes.InvalidArgument, "cannot create volume without API binding")
	}

	requestedVolumeName := req.GetName()

	mountPath := make(map[bool]string)

	filesystemName := GetFSNameFromRequest(req)
	sourceVolId := req.GetVolumeContentSource().GetVolume().GetVolumeId() // we can assume no nil pointer as the function is called only if it happens
	if sourceVolId == "" {
		return nil, status.Error(codes.InvalidArgument, "Source volume ID is empty")
	}

	sourceVol, err := NewVolumeFromId(ctx, sourceVolId, client, server)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "Failed to validate source volume ID %s", sourceVolId)
	}
	if sourceVol.hasInnerPath() && !server.getConfig().allowSnapshotsOfLegacyVolumes {
		// block cloning of snapshots from legacy volumes, as it wastes space
		return nil, status.Errorf(codes.FailedPrecondition, "Cloning is not supported for Legacy CSI volumes")
	}

	// we assume the following when cloning a volume from existing volume:
	// - VolumeType is always new
	// - fsName will remain as is
	// - innerPath will remain as is
	// - must point on the new snapshot
	// Hence:
	// - sourceVolumeID is not participating in generation of names
	// - accessPoint must be calculated as usual, from volume name
	// - snapshot name must be calculated as usual too

	exists, err := sourceVol.Exists(ctx)
	if err != nil || !exists {
		return nil, status.Error(codes.NotFound, "Source volume does not exist")
	}
	sourceVolFsName := sliceFilesystemNameFromVolumeId(sourceVolId)
	if filesystemName != "" && sourceVolFsName != filesystemName {
		logger.Error().Err(err).Str("filesystem_name", filesystemName).Str("src_volume_filesystem", sourceVolFsName).Msg("")
		return nil, status.Error(codes.InvalidArgument, "Filesystem specified in storageClass and differs from source volume filesystem, this is not supported")
	}
	volType := VolumeTypeUnified
	filesystemName = sourceVolFsName
	innerPath := sourceVol.getInnerPath()
	wekaSnapName := generateWekaSnapNameForSnapBasedVol(server.getConfig().VolumePrefix, requestedVolumeName)
	wekaSnapAccessPoint := generateWekaSnapAccessPointForSnapBasedVol(requestedVolumeName)

	volId := generateVolumeIdFromComponents(volType, filesystemName, wekaSnapAccessPoint, innerPath)
	vol := &UnifiedVolume{
		id:                  volId,
		FilesystemName:      filesystemName,
		SnapshotName:        wekaSnapName,
		SnapshotAccessPoint: wekaSnapAccessPoint,
		innerPath:           innerPath,
		apiClient:           client,
		mountPath:           mountPath,
		srcVolume:           sourceVol,
		server:              server,
	}
	return vol, nil
}
