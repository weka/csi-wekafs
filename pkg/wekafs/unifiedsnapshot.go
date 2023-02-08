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
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	MaxSnapshotDeletionDuration = time.Hour * 2 // Max time to delete snapshot
)

type UnifiedSnapshot struct {
	id                  string
	FilesystemName      string
	SnapshotNameHash    string
	SnapshotIntegrityId string
	SnapshotName        string
	innerPath           string
	SourceVolume        Volume
	srcSnapshotUid      *uuid.UUID
	apiClient           *apiclient.ApiClient

	server AnyServer
}

func (s *UnifiedSnapshot) MarshalZerologObject(e *zerolog.Event) {
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

func (s *UnifiedSnapshot) getCsiSnapshot(ctx context.Context) *csi.Snapshot {
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

func (s *UnifiedSnapshot) GetId() string {
	return s.id
}

func (s *UnifiedSnapshot) Exists(ctx context.Context) (bool, error) {
	op := "SnapshotExists"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)

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

	if s.server != nil && s.server.isInDebugMode() {
		// here comes a workaround to enable running CSI sanity in detached mode, by mimicking the directory structure as if it was a real snapshot.
		// no actual data is copied, only directory structure is created
		// happens only if the real snapshot indeed exists and is valid
		err := s.mimicDirectoryStructureForDebugMode(ctx)
		if err != nil {
			return false, err
		}
	}

	logger.Info().Msg("Snapshot exists")

	return true, nil
}

func (s *UnifiedSnapshot) Create(ctx context.Context) error {
	op := "SnapshotCreate"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
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
		return status.Errorf(codes.Internal, fmt.Sprintln("Failed to create snapshot", err.Error()))
	}
	logger.Info().Str("snapshot", s.SnapshotName).
		Str("snapshot_uid", snap.Uid.String()).
		Str("access_point", s.SnapshotIntegrityId).Msg("Snapshot was created successfully")

	if s.server != nil && s.server.isInDebugMode() {
		// here comes a workaround to enable running CSI sanity in detached mode, by mimicking the directory structure as if it was a real snapshot.
		// no actual data is copied, only directory structure is created
		// happens only if the real snapshot indeed exists and is valid
		err := s.mimicDirectoryStructureForDebugMode(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *UnifiedSnapshot) mimicDirectoryStructureForDebugMode(ctx context.Context) error {
	logger := log.Ctx(ctx)
	logger.Warn().Bool("debug_mode", true).Msg("Creating directory mimicPath inside filesystem .snapshots to mimic Weka snapshot behavior")
	const xattrMount = true // no need to have xattr mount to do that
	v := s.SourceVolume
	err, unmount := v.Mount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return err
	}
	basePath := v.getMountPath(xattrMount)
	mimicPath := filepath.Join(basePath, SnapshotsSubDirectory, s.SnapshotIntegrityId)
	log.Info().Str("mimic_path", mimicPath).Msg("Creating mimicPath")
	// make sure we don't hit umask upon creating directory
	oldMask := syscall.Umask(0)
	defer syscall.Umask(oldMask)

	if err := os.MkdirAll(mimicPath, DefaultVolumePermissions); err != nil {
		logger.Error().Err(err).Str("volume_path", mimicPath).Msg("Failed to create volume directory")
		return err
	}
	logger.Debug().Str("mimic_path", v.getFullPath(ctx, true)).Msg("Successully created directory")
	return nil

}

func (s *UnifiedSnapshot) getInnerPath() string {
	return s.innerPath
}

func (s *UnifiedSnapshot) hasInnerPath() bool {
	return s.getInnerPath() != ""
}

func (s *UnifiedSnapshot) getObject(ctx context.Context) (*apiclient.Snapshot, error) {
	op := "SnapshotGetObject"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)

	logger := log.Ctx(ctx).With().Str("snapshot_id", s.GetId()).Str("snapshot", s.SnapshotName).Logger()
	if s.apiClient == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Could not bind snapshot %s to API endpoint", s.GetId())
	}
	snap := &apiclient.Snapshot{}
	snap, err := s.apiClient.GetSnapshotByName(ctx, s.SnapshotName)
	if err == apiclient.ObjectNotFoundError {
		return nil, nil // we know that volume doesn't exist
	} else if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch snapshot object by name")
		return nil, err
	}

	return snap, nil
}

func (s *UnifiedSnapshot) Delete(ctx context.Context) error {
	op := "SnapshotDelete"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)

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

func (s *UnifiedSnapshot) waitForSnapshotDeletion(ctx context.Context, logger zerolog.Logger, retryInterval time.Duration, maxretryInterval time.Duration) (error, bool) {
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

func (s *UnifiedSnapshot) getFileSystemObject(ctx context.Context) (*apiclient.FileSystem, error) {
	op := "getFileSystemObject"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
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
