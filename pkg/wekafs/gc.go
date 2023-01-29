package wekafs

import (
	"context"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

const garbagePath = ".__internal__wekafs-async-delete"

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

func (gc *innerPathVolGc) triggerGc(ctx context.Context, fs string, apiClient *apiclient.ApiClient) {
	gc.Lock()
	defer gc.Unlock()
	if gc.isRunning[fs] {
		gc.isDeferred[fs] = true
		return
	}
	gc.isRunning[fs] = true
	go gc.purgeLeftovers(ctx, fs, apiClient)
}

func (gc *innerPathVolGc) triggerGcVolume(ctx context.Context, volume UnifiedVolume) {
	fsName := volume.FilesystemName
	gc.Lock()
	defer gc.Unlock()
	if gc.isRunning[fsName] {
		gc.isDeferred[fsName] = true
		return
	}
	gc.isRunning[fsName] = true
	gc.isDeferred[fsName] = true
	go gc.purgeVolume(ctx, volume)
}

func (gc *innerPathVolGc) purgeVolume(ctx context.Context, volume UnifiedVolume) {
	logger := log.Ctx(ctx).With().Str("volume_id", volume.GetId()).Logger()
	logger.Debug().Msg("Starting garbage collection of volume")
	fsName := volume.FilesystemName
	defer gc.finishGcCycle(ctx, fsName, volume.apiClient)
	path, err, unmount := gc.mounter.Mount(ctx, fsName, volume.apiClient)
	defer unmount()
	volumeTrashLoc := filepath.Join(path, garbagePath)
	if err := os.MkdirAll(volumeTrashLoc, DefaultVolumePermissions); err != nil {
		logger.Error().Err(err).Msg("Failed to create garbage collector directory")
	}
	fullPath := volume.getFullPath(ctx, false)
	logger.Debug().Str("full_path", fullPath).Str("volume_trash_location", volumeTrashLoc).Msg("Moving volume contents to trash")
	if err := os.Rename(fullPath, volumeTrashLoc); err == nil {
		logger.Error().Err(err).Str("full_path", fullPath).
			Str("volume_trash_location", volumeTrashLoc).Msg("Failed to move volume contents to volumeTrashLoc")
	}
	// NOTE: there is a problem of directory leaks here. If the volume innerPath is deeper than /csi-volumes/vol-name,
	// e.g. if using statically provisioned volume, we move only the deepest directory
	// so if the volume is dir/v1/<filesystem>/this/is/a/path/to/volume, we might move only the `volume`
	// but otherwise it could be risky as if we have multiple volumes we might remove other data too, e.g.
	// vol1: dir/v1/<filesystem>/this/is/a/path/to/volume, vol2: dir/v1/<filesystem>/this/is/a/path/to/another_volume

	logger.Trace().Str("purge_path", volumeTrashLoc).Msg("Purging deleted volume data")
	if err != nil {
		logger.Error().Err(err).Msg("Failed to mount filesystem for GC processing")
		return
	}
	if err := purgeDirectory(ctx, volumeTrashLoc); err != nil {
		logger.Error().Err(err).Str("purge_path", volumeTrashLoc).Msg("Failed to remove directory")
		return
	}

	logger.Debug().Msg("Volume purged")
}

func purgeDirectory(ctx context.Context, path string) error {
	logger := log.Ctx(ctx).With().Str("path", path).Logger()
	if !PathExists(path) {
		logger.Error().Str("path", path).Msg("Failed to remove existing directory")
		return nil
	}
	for !pathIsEmptyDir(path) { // to make sure that if new files still appeared during invocation
		files, err := ioutil.ReadDir(path)
		if err != nil {
			logger.Error().Err(err).Msg("GC failed to read directory contents")
			return err
		}
		for _, f := range files {
			fp := filepath.Join(path, f.Name())
			if f.IsDir() {
				if err := purgeDirectory(ctx, fp); err != nil {
					logger.Error().Err(err).Msg("")
					return err
				}
			} else if err := os.Remove(fp); err != nil {
				logger.Error().Err(err).Msg("Failed to remove directory that was used mount point")
			}
		}
	}
	return os.Remove(path)
}

func (gc *innerPathVolGc) purgeLeftovers(ctx context.Context, fs string, apiClient *apiclient.ApiClient) {
	defer gc.finishGcCycle(ctx, fs, apiClient)
	path, err, unmount := gc.mounter.Mount(ctx, fs, apiClient)
	defer unmount()
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Str("filesystem", fs).Str("path", path).Msg("Failed mounting FS for garbage collection")
		return
	}
}

func (gc *innerPathVolGc) finishGcCycle(ctx context.Context, fs string, apiClient *apiclient.ApiClient) {
	gc.Lock()
	gc.isRunning[fs] = false
	if gc.isDeferred[fs] {
		gc.isDeferred[fs] = false
		go gc.triggerGc(ctx, fs, apiClient)
	}
	gc.Unlock()
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
