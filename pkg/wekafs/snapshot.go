package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"time"
)

type Snapshot struct {
	id                  string
	FilesystemName      string
	SnapshotNameHash    string
	SnapshotIntegrityId string
	SnapshotName        string
	innerPath           string
	SourceVolume        *Volume
	srcSnapshotUid      *uuid.UUID
	apiClient           *apiclient.ApiClient

	server AnyServer
}

func (s *Snapshot) MarshalZerologObject(e *zerolog.Event) {
	srcVolId := ""
	if s.SourceVolume != nil {
		srcVolId = s.SourceVolume.GetId()
	}
	e.Str("id", s.id).
		Str("filesystem", s.FilesystemName).
		Str("snapshot_name", s.SnapshotName).
		Str("snapshot_name_hash", s.SnapshotNameHash).
		Str("snapshot_integrity_id", s.SnapshotIntegrityId).
		Str("source_volume_id", srcVolId).
		Str("inner_path", s.innerPath)
}

func (s *Snapshot) getCsiSnapshot(ctx context.Context) *csi.Snapshot {
	snapObj, err := s.getObject(ctx)
	if err != nil {
		return &csi.Snapshot{}
	}

	return &csi.Snapshot{
		SnapshotId:     s.GetId(),
		SourceVolumeId: s.SourceVolume.GetId(),
		CreationTime:   time2Timestamp(snapObj.CreationTime),
		ReadyToUse:     !snapObj.IsRemoving,
	}
}

func (s *Snapshot) GetId() string {
	return s.id
}

func (s *Snapshot) Exists(ctx context.Context) (bool, error) {
	op := "SnapshotExists"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	logger := log.Ctx(ctx).With().Str("snapshot_id", s.GetId()).Str("snapshot", s.SnapshotName).Logger()
	logger.Trace().Msg("Checking if snapshot exists")
	snapObj, err := s.getObject(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to locate snapshot object")
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
	if snapObj.AccessPoint != s.SnapshotIntegrityId {
		logger.Debug().Msg("Snapshot matches by name but not by integrity ID")
		return true, status.Error(codes.AlreadyExists, "Another snapshot with same name already exists")
	}

	logger.Info().Msg("Snapshot exists")

	return true, nil
}

func (s *Snapshot) Create(ctx context.Context) error {
	op := "SnapshotCreate"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("snapshot_id", s.GetId()).Logger()

	// check if already exists and return OK right away
	snapObj, err := s.getObject(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "Failed to check if snapshot already exists")
	}
	if snapObj != nil {
		if snapObj.AccessPoint == s.SnapshotIntegrityId {
			logger.Trace().Msg("Snapshot already exists and matches definition")
			return nil // for idempotence
		}
		return status.Errorf(codes.AlreadyExists, "Another snapshot exists with same name")
	}
	fsObj, err := s.getFileSystemObject(ctx)
	if err != nil {
		return status.Errorf(codes.NotFound, "Failed to fetch origin filesystem from the API")
	}
	if fsObj == nil {
		return status.Errorf(codes.NotFound, "Original filesystem not found on storage")
	}

	sr := &apiclient.SnapshotCreateRequest{
		Name:          s.SnapshotName,
		AccessPoint:   s.SnapshotIntegrityId,
		SourceSnapUid: s.srcSnapshotUid,
		IsWritable:    false,
		FsUid:         fsObj.Uid,
	}

	snap := &apiclient.Snapshot{}

	if err := s.apiClient.CreateSnapshot(ctx, sr, snap); err != nil {
		return status.Errorf(codes.Internal, "Failed to create snapshot: %v", err)
	}
	logger.Info().Str("snapshot", s.SnapshotName).
		Str("snapshot_uid", snap.Uid.String()).
		Str("access_point", s.SnapshotIntegrityId).Msg("Snapshot was created successfully")

	return nil
}

func (s *Snapshot) getInnerPath() string {
	return s.innerPath
}

func (s *Snapshot) hasInnerPath() bool {
	return s.getInnerPath() != ""
}

func (s *Snapshot) getObject(ctx context.Context) (*apiclient.Snapshot, error) {
	op := "SnapshotGetObject"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	logger := log.Ctx(ctx).With().Str("snapshot_id", s.GetId()).Str("snapshot", s.SnapshotName).Logger()
	if s.apiClient == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Could not bind snapshot %s to API endpoint", s.GetId())
	}
	snap := &apiclient.Snapshot{}
	snap, err := s.apiClient.GetSnapshotByName(ctx, s.SnapshotName)
	if err == apiclient.ObjectNotFoundError {
		logger.Debug().Err(err).Msg("Failed to fetch snapshot object by name, trying without prefix")
		possibleName := strings.TrimPrefix(s.SnapshotName, s.server.getConfig().SnapshotPrefix)
		snap, err2 := s.apiClient.GetSnapshotByName(ctx, possibleName)
		if err2 != nil && err2 != apiclient.ObjectNotFoundError {
			return nil, err2
		}
		if snap != nil {
			if s.SnapshotIntegrityId == snap.AccessPoint {
				logger.Info().Str("snapshot_name", snap.Name).Msg("Found an existing snapshot with different name")
				s.SnapshotName = snap.Name
				return snap, nil
			} else {
				logger.Error().Msg("Found a snapshot that partially matches by name but having unexpected access point. Conflict.")
			}
		}
		return nil, nil // we know that volume doesn't exist
	} else if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch snapshot object by name")
		return nil, err
	}

	return snap, nil
}

func (s *Snapshot) Delete(ctx context.Context) error {
	op := "SnapshotDelete"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	logger := log.Ctx(ctx).With().Str("snapshot_id", s.GetId()).Str("snapshot", s.SnapshotName).Logger()
	obj, err := s.getObject(ctx)
	if err != nil {
		if err == apiclient.ObjectNotFoundError {
			logger.Debug().Str("snapshot", s.SnapshotName).Msg("Snapshot not found, assuming repeating request")
			return nil
		}
		logger.Error().Err(err).Msg("Failed to fetch snapshot") // TODO: check how should we behave, meanwhile return the err
		return err
	}
	if obj == nil || obj.Uid == uuid.Nil {
		logger.Debug().Str("snapshot", s.SnapshotName).Msg("Snapshot not found, assuming repeating request")
		return nil
	}

	logger.Debug().Msg("Starting deletion of snapshot")
	snapd := &apiclient.SnapshotDeleteRequest{Uid: obj.Uid}

	err = s.apiClient.DeleteSnapshot(ctx, snapd)
	if err != nil {
		if _, ok := err.(*apiclient.ApiBadRequestError); ok {
			logger.Trace().Err(err).Msg("Bad request during snapshot deletion, probably already removed")
			return nil
		}
		logger.Error().Err(err).Msg("Failed to delete snapshot")
		return err
	}
	// we need to wait till it is deleted
	retryInterval := time.Second
	maxretryInterval := time.Minute

	err, done := s.waitForSnapshotDeletion(ctx, logger, retryInterval, maxretryInterval)
	if done {
		return err
	}
	return nil
}

func (s *Snapshot) waitForSnapshotDeletion(ctx context.Context, logger zerolog.Logger, retryInterval time.Duration, maxretryInterval time.Duration) (error, bool) {
	for start := time.Now(); time.Since(start) < MaxSnapshotDeletionDuration; {
		snap, err := s.getObject(ctx)
		if err != nil {
			if err == apiclient.ObjectNotFoundError {
				return nil, true
			}
		}
		if snap == nil || snap.Uid == uuid.Nil {
			logger.Trace().Msg("Snapshot was removed successfully")
			return nil, true
		} else if snap.IsRemoving {
			logger.Trace().Msg("Snapshot is still being removed")
		} else {
			return errors.New(fmt.Sprintf("Snapshot %s not marked for deletion but it should", s.SnapshotName)), true
		}
		time.Sleep(retryInterval)
		retryInterval = Min(retryInterval*2, maxretryInterval)
	}
	logger.Error().Msg("Timeout deleting snapshot")
	return nil, false
}

func (s *Snapshot) getFileSystemObject(ctx context.Context) (*apiclient.FileSystem, error) {
	op := "getFileSystemObject"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("snapshot_id", s.GetId()).Str("filesystem", s.FilesystemName).Logger()
	if s.apiClient == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Could not bind snapshot %s to API endpoint", s.GetId())
	}
	fsObj := &apiclient.FileSystem{}
	fsObj, err := s.apiClient.GetFileSystemByName(ctx, s.FilesystemName)
	if err == apiclient.ObjectNotFoundError {
		return nil, nil // we know that fs doesn't exist
	} else if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch filesystem object by name")
		return nil, err
	}
	return fsObj, nil

}
