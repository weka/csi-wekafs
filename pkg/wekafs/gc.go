package wekafs

import (
	"github.com/golang/glog"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

const garbagePath = ".__internal__wekafs-async-delete"

type dirVolumeGc struct {
	isRunning  map[string]bool
	isDeferred map[string]bool
	sync.Mutex
	mounter *wekaMounter
}

func initDirVolumeGc(mounter *wekaMounter) *dirVolumeGc {
	gc := dirVolumeGc{mounter: mounter}
	gc.isRunning = make(map[string]bool)
	gc.isDeferred = make(map[string]bool)
	return &gc
}

func (gc *dirVolumeGc) triggerGc(fs string, apiClient *apiclient.ApiClient) {
	gc.Lock()
	defer gc.Unlock()
	if gc.isRunning[fs] {
		gc.isDeferred[fs] = true
		return
	}
	gc.isRunning[fs] = true
	go gc.purgeLeftovers(fs, apiClient)
}

func (gc *dirVolumeGc) triggerGcVolume(volume DirVolume) {
	fs := volume.Filesystem
	gc.Lock()
	defer gc.Unlock()
	if gc.isRunning[fs] {
		gc.isDeferred[fs] = true
		return
	}
	gc.isRunning[fs] = true
	gc.isDeferred[fs] = true
	go gc.purgeVolume(volume)
}

func (gc *dirVolumeGc) purgeVolume(volume DirVolume) {
	fs := volume.Filesystem
	innerPath := volume.dirName
	defer gc.finishGcCycle(fs, volume.apiClient)
	path, err, unmount := gc.mounter.Mount(fs, volume.apiClient)
	defer unmount()
	fullPath := filepath.Join(path, garbagePath, innerPath)
	glog.Infof("Purging deleted volume data in %s", fullPath)
	if err != nil {
		glog.Errorf("Failed mounting FS %s for GC", fs)
	}
	if err := purgeDirectory(fullPath); err != nil {
		glog.Errorf("Failed to remove directory %s", fullPath)
	}
	glog.Infof("Directory %s was successfully deleted", fullPath)
}

func purgeDirectory(path string) error {
	if !PathExists(path) {
		glog.Warningf("GC failed to remove directory %s, not since it doesn't exist", path)
		return nil
	}
	for !pathIsEmptyDir(path) { // to make sure that if new files still appeared during invocation
		files, err := ioutil.ReadDir(path)
		if err != nil {
			glog.Infof("GC failed to read contents of %s", path)
			return err
		}
		for _, f := range files {
			fp := filepath.Join(path, f.Name())
			if f.IsDir() {
				if err := purgeDirectory(fp); err != nil {
					return err
				}
			} else if err := os.Remove(fp); err != nil {
				glog.Infof("Failed to remove entry %s", fp)
			}
		}
	}
	return os.Remove(path)
}

func (gc *dirVolumeGc) purgeLeftovers(fs string, apiClient *apiclient.ApiClient) {
	defer gc.finishGcCycle(fs, apiClient)
	path, err, unmount := gc.mounter.Mount(fs, apiClient)
	defer unmount()
	if err != nil {
		glog.Errorf("Failed mounting FS %s for GC", fs)
		return
	}

	glog.Warningf("TODO: GC filesystem in %s", path)
}

func (gc *dirVolumeGc) finishGcCycle(fs string, apiClient *apiclient.ApiClient) {
	gc.Lock()
	gc.isRunning[fs] = false
	if gc.isDeferred[fs] {
		gc.isDeferred[fs] = false
		go gc.triggerGc(fs, apiClient)
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
