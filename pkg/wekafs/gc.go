package wekafs

import (
	"github.com/golang/glog"
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

func initDirVolumeGc() *dirVolumeGc {
	gc := dirVolumeGc{}
	gc.isRunning = make(map[string]bool)
	gc.isDeferred = make(map[string]bool)
	return &gc
}

func (gc *dirVolumeGc) triggerGc(fs string) {
	gc.Lock()
	defer gc.Unlock()
	if gc.isRunning[fs] {
		gc.isDeferred[fs] = true
		return
	}
	gc.isRunning[fs] = true
	go gc.purgeLeftovers(fs)
}

func (gc *dirVolumeGc) triggerGcVolume(volume dirVolume) {
	fs := volume.fs
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

func (gc *dirVolumeGc) purgeVolume(volume dirVolume) {
	fs := volume.fs
	innerPath := volume.dirName
	defer gc.finishGcCycle(fs)
	path, err, unmount := gc.mounter.Mount(fs)
	defer unmount()
	if err != nil {
		glog.Errorf("Failed mounting FS %s for GC", fs)
		return
	}

	glog.Warningf("TODO: GC Volume/path %s", filepath.Join(path, innerPath)) //TODO: To implement deletion of single volume
}

func (gc *dirVolumeGc) purgeLeftovers(fs string) {
	defer gc.finishGcCycle(fs)
	path, err, unmount := gc.mounter.Mount(fs)
	defer unmount()
	if err != nil {
		glog.Errorf("Failed mounting FS %s for GC", fs)
		return
	}

	glog.Warningf("TODO: GC Volume in %s", path) //TODO: To implement deletion of whole garbage folder
}

func (gc *dirVolumeGc) finishGcCycle(fs string) {
	gc.Lock()
	gc.isRunning[fs] = false
	if gc.isDeferred[fs] {
		gc.isDeferred[fs] = false
		go gc.triggerGc(fs)
	}
	gc.Unlock()
}
