package wekafs

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.opentelemetry.io/otel"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

type innerPathVolGc struct {
	isRunning  map[string]bool
	isDeferred map[string]bool
	sync.Mutex
	mounter AnyMounter
	config  *DriverConfig
}

func initInnerPathVolumeGc(mounter AnyMounter) *innerPathVolGc {
	gc := innerPathVolGc{mounter: mounter}
	gc.isRunning = make(map[string]bool)
	gc.isDeferred = make(map[string]bool)
	return &gc
}

func (gc *innerPathVolGc) triggerGcVolume(ctx context.Context, volume *Volume) error {
	op := "triggerGcVolume"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("volume_id", volume.GetId()).Logger()
	logger.Info().Msg("Triggering garbage collection of volume")
	return gc.moveVolumeToTrash(ctx, volume)
}

func (gc *innerPathVolGc) moveVolumeToTrash(ctx context.Context, volume *Volume) error {
	op := "moveVolumeToTrash"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("volume_id", volume.GetId()).Logger()
	fsName := volume.FilesystemName

	if gc.config.skipGarbageCollection {
		logger.Debug().Msg("Moving volume to trash, skipping garbage collection according to configuration")
	} else {
		logger.Debug().Msg("Moving volume to trash and starting garbage collection")
		defer gc.initiateGarbageCollection(ctx, fsName, volume.apiClient)
	}

	path, err, unmount := gc.mounter.Mount(ctx, fsName, volume.apiClient)
	defer unmount()
	if err != nil {
		logger.Error().Err(err).Msg("Failed to mount filesystem for GC processing")
		return err
	}
	volumeTrashLoc := filepath.Join(path, garbagePath)
	fullPath := filepath.Join(path, volume.GetFullPath(ctx))
	if !PathExists(fullPath) {
		logger.Debug().Str("full_path", fullPath).Msg("Volume contents not found, maybe already moved to trash, skipping")
		return nil
	}

	newPath := filepath.Join(volumeTrashLoc, filepath.Base(fullPath))
	logger.Debug().Str("full_path", fullPath).Str("volume_trash_location", volumeTrashLoc).Msg("Moving volume contents to trash")

	gc.Lock()
	defer gc.Unlock()
	if err := os.MkdirAll(volumeTrashLoc, DefaultVolumePermissions); err != nil {
		if !os.IsExist(err) {
			logger.Error().Str("garbage_collection_path", volumeTrashLoc).Err(err).Msg("Failed to create garbage collector directory")
			return err
		}
	}
	if err := os.Rename(fullPath, newPath); err != nil {
		logger.Error().Err(err).Str("full_path", fullPath).
			Str("volume_trash_location", volumeTrashLoc).Msg("Failed to move volume contents to volumeTrashLoc")
		return err
	}

	// NOTE: there is a problem of directory leaks here. If the volume innerPath is deeper than /csi-volumes/vol-name,
	// e.g. if using statically provisioned volume, we move only the deepest directory
	// so if the volume is dir/v1/<filesystem>/this/is/a/path/to/volume, we might move only the `volume`
	// but otherwise it could be risky as if we have multiple volumes we might remove other data too, e.g.
	// vol1: dir/v1/<filesystem>/this/is/a/path/to/volume, vol2: dir/v1/<filesystem>/this/is/a/path/to/another_volume
	// 2024-07-29: apparently seems this is not a real problem since static volumes are not deleted this way
	//             and dynamic volumes are always created inside the /csi-volumes
	logger.Debug().Str("full_path", fullPath).Str("volume_trash_location", volumeTrashLoc).Msg("Volume contents moved to trash")
	return nil
}

func (gc *innerPathVolGc) purgeLeftovers(ctx context.Context, fs string, apiClient *apiclient.ApiClient) {
	op := "purgeLeftovers"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	gc.Lock()
	gc.isRunning[fs] = true
	gc.Unlock()
	path, err, unmount := gc.mounter.Mount(ctx, fs, apiClient)
	defer unmount()
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Str("filesystem", fs).Str("path", path).Msg("Failed mounting FS for garbage collection")
		return
	}
	volumeTrashLoc := filepath.Join(path, garbagePath)

	if fileExists("/locar") {
		logger.Debug().Msg("Using locar for fast deletion")
		deleteCmd := exec.Command("bash", "-c",
			fmt.Sprintf("/locar --type file %s | xargs -P128 -n128 rm -f 2>&1 | wc -l; /locar --type dir %s | /usr/bin/xargs -P128 -n128 rm -rf 2>&1 | wc -l", volumeTrashLoc, volumeTrashLoc),
		)
		output, err := deleteCmd.CombinedOutput()
		if err != nil {
			logger.Error().Err(err).Msg("Error running locar")
		}
		logger.Trace().Str("output", string(output)).Msg("Locar output")
	} else {
		logger.Debug().Msg("Using default deletion method")
		if err := os.RemoveAll(volumeTrashLoc); err != nil {
			logger.Error().Err(err).Str("path", volumeTrashLoc).Msg("Failed to perform garbage collection")
		}
	}
	logger.Debug().Msg("Garbage collection completed")
	gc.Lock()
	defer gc.Unlock()
	gc.isRunning[fs] = false
	if gc.isDeferred[fs] {
		gc.isDeferred[fs] = false
		go gc.purgeLeftovers(ctx, fs, apiClient)
	}
}

func (gc *innerPathVolGc) initiateGarbageCollection(ctx context.Context, fs string, apiClient *apiclient.ApiClient) {
	op := "initiateGarbageCollection"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	logger.Trace().Msg("Initiating garbage collection")
	gc.Lock()
	defer gc.Unlock()
	if gc.isRunning[fs] {
		logger.Trace().Msg("Garbage collection already running, deferring next run")
		gc.isDeferred[fs] = true
		return
	}
	if !gc.isDeferred[fs] {
		logger.Trace().Msg("Garbage collection not running, starting")
		go gc.purgeLeftovers(ctx, fs, apiClient)
	}
}

// pathIsEmptyDir is a simple check to determine if directory is empty or not.
func pathIsEmptyDir(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()

	_, err = f.Readdir(1)
	return err == io.EOF
}
