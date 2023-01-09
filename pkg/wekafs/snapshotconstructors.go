package wekafs

import (
	"context"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

func NewSnapshotFromVolumeCreate(ctx context.Context, name string, sourceVolume Volume, apiClient *apiclient.ApiClient) (Snapshot, error) {
	srcVolId := sourceVolume.GetId()
	logger := log.Ctx(ctx).With().Str("src_volume_id", srcVolId).Str("snapshot_name", name).Logger()
	logger.Trace().Msg("Initializating snapshot object")
	if apiClient != nil {
		logger.Trace().Msg("Successfully bound volume to backend API client")
	}
	hash := calculateSnapshotParamsHash(name, sourceVolume.GetId())
	snap := &UnifiedSnapshot{
		paramsHash: &hash,
		srcVolume:  sourceVolume,
		apiClient:  apiClient,
	}
	logger = log.Ctx(ctx).With().Str("snapshot_id", snap.GetId()).Logger()
	logger.Trace().Msg("Successfully initialized object")
	return snap, nil
}

func NewSnapshotFromId(ctx context.Context, id string, apiClient *apiclient.ApiClient) (Snapshot, error) {
	logger := log.Ctx(ctx).With().Str("snapshot_id", id).Logger()
	logger.Trace().Msg("Initializating snapshot object")
	if err := validateSnapshotId(id); err != nil {
		return &UnifiedSnapshot{}, err
	}
	if apiClient != nil {
		logger.Trace().Msg("Successfully bound volume to backend API client")
	}
	Uid := GetSnapshotUuid(id)
	paramsHash := GetSnapshotParamsHash(id)
	s := &UnifiedSnapshot{
		id:         &id,
		Uid:        Uid,
		paramsHash: &paramsHash,
		apiClient:  apiClient,
	}
	logger.Trace().Msg("Successfully initialized object")
	return s, nil
}
