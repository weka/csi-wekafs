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
	innerPath := volume.getInnerPath()
	defer gc.finishGcCycle(ctx, fsName, volume.apiClient)
	path, err, unmount := gc.mounter.Mount(ctx, fsName, volume.apiClient)
	defer unmount()
	fullPath := filepath.Join(path, garbagePath, innerPath)
	logger.Trace().Str("full_path", fullPath).Msg("Purging deleted volume data")
	if err != nil {
		logger.Error().Err(err).Msg("Failed to mount filesystem for GC processing")
		return
	}
	if err := purgeDirectory(ctx, fullPath); err != nil {
		logger.Error().Err(err).Str("full_path", fullPath).Msg("Failed to remove directory")
		return
	}

	logger.Debug().Str("full_path", fullPath).Msg("Directory was successfully deleted")
}

func purgeDirectory(ctx context.Context, path string) error {
	logger := log.Ctx(ctx).With().Str("path", path).Logger()
	if !PathExists(path) {
		logger.Error().Msg("Failed to remove existing directory")
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
