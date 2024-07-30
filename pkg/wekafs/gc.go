package wekafs

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.opentelemetry.io/otel"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const garbagePath = ".__internal__wekafs-async-delete"

//const garbageCollectionMaxThreads = 32

type innerPathVolGc struct {
	isRunning  map[string]bool
	isDeferred map[string]bool
	sync.Mutex
	mounter *wekaMounter
}

func initInnerPathVolumeGc(mounter *wekaMounter) *innerPathVolGc {
	gc := innerPathVolGc{mounter: mounter}
	gc.isRunning = make(map[string]bool)
	gc.isDeferred = make(map[string]bool)
	return &gc
}

func (gc *innerPathVolGc) triggerGcVolume(ctx context.Context, volume *Volume) {
	op := "triggerGcVolume"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("volume_id", volume.GetId()).Logger()
	logger.Info().Msg("Triggering garbage collection of volume")
	fsName := volume.FilesystemName
	gc.moveVolumeToTrash(ctx, volume) // always do it synchronously
	go gc.initiateGarbageCollection(ctx, fsName, volume.apiClient)
}

func (gc *innerPathVolGc) moveVolumeToTrash(ctx context.Context, volume *Volume) {
	op := "moveVolumeToTrash"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("volume_id", volume.GetId()).Logger()
	logger.Debug().Msg("Starting garbage collection of volume")
	fsName := volume.FilesystemName
	defer gc.initiateGarbageCollection(ctx, fsName, volume.apiClient)
	path, err, unmount := gc.mounter.Mount(ctx, fsName, volume.apiClient)
	defer unmount()
	if err != nil {
		logger.Error().Err(err).Msg("Failed to mount filesystem for GC processing")
		return
	}
	volumeTrashLoc := filepath.Join(path, garbagePath)
	if err := os.MkdirAll(volumeTrashLoc, DefaultVolumePermissions); err != nil {
		logger.Error().Str("garbage_collection_path", volumeTrashLoc).Err(err).Msg("Failed to create garbage collector directory")
	} else {
		logger.Debug().Str("garbage_collection_path", volumeTrashLoc).Msg("Successfully created garbage collection directory")
	}
	fullPath := filepath.Join(path, volume.GetFullPath(ctx))
	logger.Debug().Str("full_path", fullPath).Str("volume_trash_location", volumeTrashLoc).Msg("Moving volume contents to trash")
	newPath := filepath.Join(volumeTrashLoc, filepath.Base(fullPath))
	if err := os.Rename(fullPath, newPath); err != nil {
		logger.Error().Err(err).Str("full_path", fullPath).
			Str("volume_trash_location", volumeTrashLoc).Msg("Failed to move volume contents to volumeTrashLoc")
	}
	// NOTE: there is a problem of directory leaks here. If the volume innerPath is deeper than /csi-volumes/vol-name,
	// e.g. if using statically provisioned volume, we move only the deepest directory
	// so if the volume is dir/v1/<filesystem>/this/is/a/path/to/volume, we might move only the `volume`
	// but otherwise it could be risky as if we have multiple volumes we might remove other data too, e.g.
	// vol1: dir/v1/<filesystem>/this/is/a/path/to/volume, vol2: dir/v1/<filesystem>/this/is/a/path/to/another_volume
	// 2024-07-29: apparently seems this is not a real problem since static volumes are not deleted this way
	//             and dynamic volumes are always created inside the /csi-volumes
	logger.Debug().Str("full_path", fullPath).Str("volume_trash_location", volumeTrashLoc).Msg("Volume contents moved to trash")
}

func deleteDirectoryWorker(paths <-chan string, wg *sync.WaitGroup) {
	defer wg.Done()
	for path := range paths {
		err := os.RemoveAll(path)
		if err != nil {
			fmt.Printf("Failed to remove %s: %s\n", path, err)
		}
	}
}

//func deleteDirectoryTree(ctx context.Context, path string) error {
//	op := "deleteDirectoryTree"
//	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
//	defer span.End()
//	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
//	logger := log.Ctx(ctx).With().Str("path", path).Logger()
//	paths := make(chan string, garbageCollectionMaxThreads)
//	var wg sync.WaitGroup
//
//	// Start deleteDirectoryWorker goroutines
//	for i := 0; i < garbageCollectionMaxThreads; i++ {
//		wg.Add(1)
//		go deleteDirectoryWorker(paths, &wg)
//	}
//
//	// Walk the directory tree and send paths to the workers
//	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
//		if err != nil {
//			return err
//		}
//		paths <- path
//		return nil
//	})
//	if err != nil {
//		close(paths)
//		logger.Trace().Msg("Waiting for deletion workers to finish")
//		wg.Wait()
//		return err
//	}
//
//	// Close the paths channel and wait for all workers to finish
//	close(paths)
//	wg.Wait()
//
//	return nil
//}

func deleteDirectoryRecursively(ctx context.Context, path string, wg *sync.WaitGroup, errChan chan<- error, sem chan struct{}) {
	op := "deleteDirectoryRecursively"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("path", path).Logger()

	dir, err := os.Open(path)
	if err != nil {
		errChan <- fmt.Errorf("failed to open directory %s: %v", path, err)
		return
	}
	defer dir.Close()

	for {
		names, err := dir.Readdirnames(100) // Read directory entries in chunks
		if err != nil {
			if err != io.EOF {
				logger.Error().Err(err).Msg("Failed to read directory entries")
				errChan <- fmt.Errorf("failed to read directory entries: %v", err)
			}
			break
		}
		if len(names) == 0 {
			break
		}
		logger.Trace().Int("num_entries", len(names)).Msg("Processing directory entries")
		for _, name := range names {
			subPath := filepath.Join(path, name)
			go func(p string) {
				logger.Trace().Str("sub_path", p).Msg("Processing subpath, acquiring semaphore")
				sem <- struct{}{} // Acquire semaphore
				logger.Trace().Str("sub_path", p).Msg("Processing subpath, acquired semaphore")
				wg.Add(1)
				defer wg.Done()
				defer func() { <-sem }() // Release semaphore
				fi, err := os.Lstat(p)
				if err != nil {
					logger.Error().Err(err).Str("path", p).Msg("Failed to stat path")
					errChan <- fmt.Errorf("failed to stat %s: %v", p, err)
					return
				}
				if fi.IsDir() {
					deleteDirectoryRecursively(ctx, p, wg, errChan, sem) // Recurse into subdirectory
					if err := os.Remove(p); err != nil {
						errChan <- fmt.Errorf("failed to remove entry %s: %v", p, err)
					}
				}
			}(subPath)
		}
	}
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

	var wg sync.WaitGroup
	errChan := make(chan error, 10000)
	sem := make(chan struct{}, 1000)
	go deleteDirectoryRecursively(ctx, volumeTrashLoc, &wg, errChan, sem)

	time.Sleep(3 * time.Second) // Wait for some time to allow the workers to start
	wg.Wait()

	if err := os.Remove(path); err != nil {
		errChan <- fmt.Errorf("failed to remove file %s: %v", path, err)
	}

	close(errChan)
	for err := range errChan {
		if err != nil {
			logger.Warn().Err(err).Msg("Error occured during deletion")
		}
	}

	//
	//err = deleteDirectoryTree(ctx, volumeTrashLoc)
	//if err != nil {
	//	logger.Error().Err(err).Msg("Failed to remove directory tree")
	//} else {
	//	logger.Trace().Msg("Directory tree deleted successfully")
	//}

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
