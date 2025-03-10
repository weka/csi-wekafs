package wekafs

import (
	"context"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

func NewSnapshotFromVolumeCreate(ctx context.Context, name string, sourceVolume *Volume, apiClient *apiclient.ApiClient, server AnyServer) (*Snapshot, error) {
	srcVolId := sourceVolume.GetId()
	logger := log.Ctx(ctx).With().Str("src_volume_id", srcVolId).Str("snapshot_name", name).Logger()
	logger.Trace().Msg("Initializating snapshot object")
	if apiClient != nil {
		logger.Trace().Msg("Successfully bound snapshot to backend API client")
	}

	filesystemName := sliceFilesystemNameFromVolumeId(srcVolId)
	snapNameHash := generateSnapshotNameHash(name)
	snapIntegrityId := generateSnapshotIntegrityID(name, srcVolId)
	snapName := generateWekaSnapNameForSnapshot(server.getConfig().SnapshotPrefix, name)
	innerPath := sliceInnerPathFromVolumeId(srcVolId)
	snapshotId := generateSnapshotIdFromComponents(SnapshotTypeUnifiedSnap, filesystemName, snapNameHash, snapIntegrityId, innerPath)
	var sourceSnapUid *uuid.UUID
	if sourceVolume != nil && sourceVolume.isOnSnapshot() {
		obj, err := sourceVolume.getSnapshotObj(ctx, false)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to fetch content object of source volume")
			return nil, err
		}
		sourceSnapUid = &(obj.Uid)
	}
	s := &Snapshot{
		id:                  snapshotId,
		FilesystemName:      filesystemName,
		SnapshotNameHash:    snapNameHash,
		SnapshotIntegrityId: snapIntegrityId,
		SnapshotName:        snapName,
		innerPath:           innerPath,
		SourceVolume:        sourceVolume,
		srcSnapshotUid:      sourceSnapUid,
		apiClient:           apiClient,
		server:              server,
	}
	logger = log.Ctx(ctx).With().Str("snapshot_id", s.GetId()).Logger()
	logger.Trace().Object("snap_info", s).Msg("Successfully initialized object")
	return s, nil
}

func NewSnapshotFromId(ctx context.Context, snapshotId string, apiClient *apiclient.ApiClient, server AnyServer) (*Snapshot, error) {
	logger := log.Ctx(ctx).With().Str("snapshot_id", snapshotId).Logger()
	logger.Trace().Msg("Initializating snapshot object")
	if err := validateSnapshotId(snapshotId); err != nil {
		logger.Error().Msg("Failed to validate snapshot_id")
		return &Snapshot{}, err
	}
	if apiClient != nil {
		logger.Trace().Msg("Successfully bound snapshot to backend API client")
	}
	s := &Snapshot{
		id:                  snapshotId,
		FilesystemName:      sliceFilesystemNameFromSnapshotId(snapshotId),
		SnapshotNameHash:    sliceSnapshotNameHashFromSnapshotId(snapshotId),
		SnapshotIntegrityId: sliceSnapshotIntegrityIdFromSnapshotId(snapshotId),
		SnapshotName:        server.getConfig().SnapshotPrefix + sliceSnapshotNameHashFromSnapshotId(snapshotId),
		innerPath:           sliceInnerPathFromSnapshotId(snapshotId),
		apiClient:           apiClient,
		server:              server,
	}
	logger.Trace().Object("snap_info", s).Msg("Successfully initialized object")
	return s, nil
}
