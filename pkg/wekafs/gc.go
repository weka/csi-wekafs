package wekafs

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.opentelemetry.io/otel"
)

const garbagePath = ".__internal__wekafs-async-delete"

const (
	// garbageCollectionTimeout bounds a single purge cycle so a hung mount cannot
	// block the detached GC goroutine indefinitely.
	garbageCollectionTimeout = 10 * time.Minute
	// garbageCollectionRetryBackoff delays a retry after a failed purge to avoid hot-looping.
	garbageCollectionRetryBackoff = time.Minute
)

//const garbageCollectionMaxThreads = 32

// gcKey identifies one filesystem's trash on one Weka API client.
//
// Filesystem names are only unique within a tenant, so with multitenancy two tenants can each have
// a filesystem called "default" that are entirely different objects. Keying the GC state by name
// alone made one tenant's purge look like it covered the other's: the second tenant's request would
// see the name as already running, mark it deferred, and the chained re-run would then purge the
// first tenant's filesystem again - leaving the second tenant's trash behind indefinitely.
type gcKey struct {
	apiClientHash uint32
	filesystem    string
}

// newGcKey scopes a filesystem to its API client. A nil client is legacy, API-unbound mode, where
// there is only one cluster in play and a zero hash is the right shared scope.
func newGcKey(fs string, apiClient *apiclient.ApiClient) gcKey {
	key := gcKey{filesystem: fs}
	if apiClient != nil {
		key.apiClientHash = apiClient.Hash()
	}
	return key
}

type innerPathVolGc struct {
	isRunning  map[gcKey]bool
	isDeferred map[gcKey]bool
	sync.Mutex
	mounter AnyMounter
	config  *DriverConfig
}

func initInnerPathVolumeGc(mounter AnyMounter) *innerPathVolGc {
	gc := innerPathVolGc{mounter: mounter}
	gc.isRunning = make(map[gcKey]bool)
	gc.isDeferred = make(map[gcKey]bool)
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

func (gc *innerPathVolGc) moveVolumeToTrash(ctx context.Context, volume *Volume) (retErr error) {
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
	defer deferUmount(unmount, &retErr)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to mount filesystem for GC processing")
		return err
	}
	volumeTrashLoc := filepath.Join(path, garbagePath)
	if err := os.MkdirAll(volumeTrashLoc, DefaultVolumePermissions); err != nil {
		if !os.IsExist(err) {
			logger.Error().Str("garbage_collection_path", volumeTrashLoc).Err(err).Msg("Failed to create garbage collector directory")
			return err
		}
	}
	fullPath := filepath.Join(path, volume.GetFullPath(ctx))
	logger.Debug().Str("full_path", fullPath).Str("volume_trash_location", volumeTrashLoc).Msg("Moving volume contents to trash")
	newPath := filepath.Join(volumeTrashLoc, filepath.Base(fullPath))
	if !fileExists(fullPath) {
		logger.Debug().Str("full_path", fullPath).Msg("Volume contents not found, maybe already moved to trash, skipping")
		return nil
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

	// The caller claimed this key before spawning the goroutine, so only one purge per filesystem
	// per API client is ever in flight.
	key := newGcKey(fs, apiClient)
	// Carry the tenant scope on every line: under multitenancy the filesystem name alone no longer
	// identifies which trash a message is about.
	scoped := logger.With().Str("filesystem", fs).Uint32("api_client", key.apiClientHash).Logger()
	logger = &scoped

	succeeded := false
	// Release the claim on every exit path. Chain another run if one was deferred while we ran, or
	// retry (after a backoff) if this run failed, so failures are retried instead of silently
	// stranding the trash and wedging GC.
	defer func() {
		gc.Lock()
		defer gc.Unlock()
		if gc.isDeferred[key] {
			gc.isDeferred[key] = false
			// Hand the claim straight to the chained run rather than releasing it, so no other
			// caller can start a competing purge in the gap.
			go gc.purgeLeftovers(ctx, fs, apiClient)
			return
		}
		gc.isRunning[key] = false
		if !succeeded {
			go func() {
				time.Sleep(garbageCollectionRetryBackoff)
				gc.initiateGarbageCollection(ctx, fs, apiClient)
			}()
		}
	}()

	// Bound a single purge cycle. Derived from the (already detached) ctx, so the
	// deferred re-run above still starts from a fresh, non-expired context.
	opCtx, cancel := context.WithTimeout(ctx, garbageCollectionTimeout)
	defer cancel()

	path, err, unmount := gc.mounter.Mount(opCtx, fs, apiClient)
	defer func() {
		if uErr := unmount(); uErr != nil {
			logger.Error().Err(uErr).Str("path", path).Msg("Failed to release filesystem mount after garbage collection")
		}
	}()
	if err != nil {
		logger.Error().Err(err).Str("path", path).Msg("Failed mounting FS for garbage collection")
		return
	}
	volumeTrashLoc := filepath.Join(path, garbagePath)

	// locar empties the trash far faster than a single-threaded walk, but --delete-all only
	// removes what it finds inside the directory, never the directory itself. Skip it when there
	// is no trash yet, since locar treats a missing path as an error.
	if fileExists("/locar") && PathExists(volumeTrashLoc) {
		logger.Debug().Msg("Using locar for fast deletion")
		output, err := exec.CommandContext(opCtx, "/locar", "--delete-all", volumeTrashLoc).CombinedOutput()
		if err != nil {
			logger.Error().Err(err).Str("output", string(output)).Msg("Error running locar")
			return
		}
		logger.Trace().Str("output", string(output)).Msg("Locar output")
	}

	// Removes just the emptied trash directory after a locar pass, or the whole tree without one.
	if err := os.RemoveAll(volumeTrashLoc); err != nil {
		logger.Error().Err(err).Str("path", volumeTrashLoc).Msg("Failed to perform garbage collection")
		return
	}
	succeeded = true
	logger.Debug().Msg("Garbage collection completed")
}

func (gc *innerPathVolGc) initiateGarbageCollection(ctx context.Context, fs string, apiClient *apiclient.ApiClient) {
	op := "initiateGarbageCollection"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	logger.Trace().Msg("Initiating garbage collection")

	// purgeLeftovers runs in a detached goroutine that outlives the originating
	// gRPC request. Strip cancellation/deadline from the request context (while
	// preserving logger and trace values) so the request returning — which fires
	// its deferred cancel() — does not abort the background purge mid-mount (CSI-422).
	bgCtx := context.WithoutCancel(ctx)

	// Scope the state to the API client as well as the filesystem name, so that two tenants sharing
	// a filesystem name do not share GC state.
	key := newGcKey(fs, apiClient)

	gc.Lock()
	defer gc.Unlock()
	if gc.isRunning[key] {
		logger.Trace().Msg("Garbage collection already running, deferring next run")
		gc.isDeferred[key] = true
		return
	}
	if !gc.isDeferred[key] {
		logger.Trace().Msg("Garbage collection not running, starting")
		// Claim the filesystem before spawning, while still holding the lock. The goroutine cannot
		// claim it itself: between the go statement and the goroutine acquiring the lock, another
		// caller would still see the filesystem as idle and start a second, concurrent purge of the
		// same trash directory.
		gc.isRunning[key] = true
		go gc.purgeLeftovers(bgCtx, fs, apiClient)
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
