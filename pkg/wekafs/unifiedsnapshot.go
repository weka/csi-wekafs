package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

const (
	WekaSnapshotNamePrefixForSnapshots = "csisnap-"
	MaxSnapshotDeletionDuration        = time.Hour * 2 // Max time to delete snapshot
)

type UnifiedSnapshot struct {
	id         *string
	Uid        *uuid.UUID
	paramsHash *string

	srcVolume Volume
	apiClient *apiclient.ApiClient
}

func (s *UnifiedSnapshot) String() string {
	return "SNAPSHOT ID: " + s.GetId() + " paramsHash: " + s.getParamsHash() + " Uid: " + s.GetUid().String()
}

func (s *UnifiedSnapshot) getCsiSnapshot(ctx context.Context) *csi.Snapshot {
	snapObj, err := s.getObject(ctx)
	if err != nil {
		return &csi.Snapshot{}
	}

	return &csi.Snapshot{
		SnapshotId:     s.GetId(),
		SourceVolumeId: s.srcVolume.GetId(),
		CreationTime:   time2Timestamp(snapObj.CreationTime),
		ReadyToUse:     !snapObj.IsRemoving,
	}
}

func (s *UnifiedSnapshot) GetId() string {
	if s.id != nil {
		return *s.id
	}
	return ""
}

func (s *UnifiedSnapshot) GetUid() uuid.UUID {
	if s.Uid != nil {
		return *s.Uid
	}
	return uuid.Nil
}

func (s *UnifiedSnapshot) Exists(ctx context.Context) (bool, error) {
	logger := log.Ctx(ctx).With().Str("snapshot_id", s.GetId()).Str("snapshot", s.getInternalSnapName()).Logger()
	logger.Trace().Msg("Checking if snapshot exists")
	snapObj, err := s.getObject(ctx)
	if err != nil {
		logger.Error().Err(err).Str("snapshot", s.getInternalSnapName()).Msg("Failed to locate snapshot object")
		return false, err
	}
	if snapObj == nil || snapObj.Uid == uuid.Nil {
		logger.Debug().Msg("Snapshot does not exist")
		return false, nil
	}
	if snapObj.IsRemoving {
		logger.Debug().Msg("Snapshot exists but is in removing state")
		return false, nil
	}
	logger.Info().Msg("Snapshot exists")
	return true, nil
}

func (s *UnifiedSnapshot) Create(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("snapshot_id", s.GetId()).Logger()

	// check if source FS actually exists
	srcVol, err := s.srcVolume.getFilesystemObj(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "Failed to check for existence of source volume")
	}
	if srcVol == nil || srcVol.Uid == uuid.Nil {
		return status.Errorf(codes.InvalidArgument, "Source volume was not found")
	}

	// check if already exists and return OK right away
	snapObj, err := s.getObject(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "Failed to check if snapshot already exists")
	}
	if snapObj != nil {
		s.generateIdAfterCreation() //TODO: FIX with latest Maor's changes
		logger.Trace().Msg("Snapshot already exists and matches definition")
		return nil // for idempotence
	}

	sr := &apiclient.SnapshotCreateRequest{
		Name:          s.getInternalSnapName(),
		AccessPoint:   s.getInternalAccessPoint(),
		SourceSnapUid: nil,
		IsWritable:    false,
		FsUid:         srcVol.Uid, // TODO: possible error, need to improve logic of checking existence to avoid 2 API different calls
	}

	snap := &apiclient.Snapshot{}

	if err := s.apiClient.CreateSnapshot(ctx, sr, snap); err != nil {
		return status.Errorf(codes.Internal, fmt.Sprintln("Failed to create snapshot", err.Error()))
	}
	s.Uid = &(snap.Uid)
	s.generateIdAfterCreation()
	logger.Info().Str("snapshot", s.getInternalSnapName()).
		Str("snapshot_uid", s.GetUid().String()).
		Str("access_point", s.getInternalAccessPoint()).Msg("Snapshot was created successfully")
	return nil
}

func (s *UnifiedSnapshot) getInnerPath() string {
	if s.srcVolume != nil {
		return s.srcVolume.getInnerPath()
	}
	return GetSnapshotInternalPath(*s.id)
}

func (s *UnifiedSnapshot) getParamsHash() string {
	return *s.paramsHash
}
func (s *UnifiedSnapshot) generateIdAfterCreation() {
	fsName := GetFSName(s.srcVolume.GetId())
	hash := s.getParamsHash()
	id := string(VolumeTypeUnifiedSnap) + "/" + fsName + ":" + s.Uid.String()

	innerPath := s.getInnerPath()
	if innerPath != "" {
		id += "/" + innerPath
	}
	id += ":" + hash
	s.id = &id
}

func (s *UnifiedSnapshot) updateAfterDeletion() {
	s.id = nil
	s.Uid = nil
}

func (s *UnifiedSnapshot) getObject(ctx context.Context) (*apiclient.Snapshot, error) {
	logger := log.Ctx(ctx).With().Str("snapshot_id", s.GetId()).Logger()
	if s.apiClient == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Could not bind snapshot %s to API endpoint", s.GetId())
	}
	snap := &apiclient.Snapshot{}
	snap, err := s.apiClient.GetSnapshotByName(ctx, s.getInternalSnapName())
	if err == apiclient.ObjectNotFoundError {
		return nil, nil // we know that volume doesn't exist
	} else if err != nil {
		logger.Error().Err(err).Str("snapshot", s.getInternalSnapName()).Msg("Failed to fetch snapshot object by name")
		return nil, err
	}
	if snap.Uid != uuid.Nil {
		s.Uid = &snap.Uid
	}

	return snap, nil
}

func (s *UnifiedSnapshot) Delete(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("snapshot_id", s.GetId()).Logger()
	exists, err := s.Exists(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch snapshot")
		return err
	}
	internalSnapName := s.getInternalSnapName()
	if !exists {
		logger.Info().Str("snapshot", internalSnapName).Msg("Could not find snapshot, probably already deleted")
		return nil
	}

	logger.Debug().Msg("Starting deletion of snapshot")
	snapd := &apiclient.SnapshotDeleteRequest{Uid: *s.Uid}

	err = s.apiClient.DeleteSnapshot(ctx, snapd)
	if err != nil {
		if err == apiclient.ObjectNotFoundError {
			logger.Debug().Str("snapshot", internalSnapName).Msg("Snapshot not found, assuming repeating request")
			return nil
		}
		logger.Error().Err(err).Str("snapshot", internalSnapName).Msg("Failed to delete snapshot")
		return err
	}
	// we need to wait till it is deleted
	retryInterval := time.Second
	maxretryInterval := time.Minute

	for start := time.Now(); time.Since(start) < MaxSnapshotDeletionDuration; {
		snap, err := s.getObject(ctx)
		if err != nil {
			if err == apiclient.ObjectNotFoundError {
				return nil
			}
		}
		if snap == nil || snap.Uid == uuid.Nil {
			logger.Trace().Str("snapshot", internalSnapName).Msg("Snapshot was removed successfully")
			return nil
		} else if snap.IsRemoving {
			logger.Trace().Str("snapshot", internalSnapName).Msg("Snapshot is still being removed")
		} else {
			return errors.New(fmt.Sprintf("Snapshot %s not marked for deletion but it should", internalSnapName))
		}
		time.Sleep(retryInterval)
		retryInterval = Min(retryInterval*2, maxretryInterval)
	}
	logger.Error().Str("snapshot", internalSnapName).Msg("Timeout deleting snapshot")
	return nil
}

func (s *UnifiedSnapshot) getSourceVolumeId() string {
	return s.srcVolume.GetId()
}

func (s *UnifiedSnapshot) getInternalSnapName() string {
	return WekaSnapshotNamePrefixForSnapshots + s.getParamsHash()[MaxHashLengthForObjectNames:]
}

func (s *UnifiedSnapshot) getInternalAccessPoint() string {
	return s.getParamsHash()[:MaxHashLengthForObjectNames]
}
