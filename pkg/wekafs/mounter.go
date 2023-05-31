package wekafs

import (
	"context"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/utils/mount"
	"sync"
	"time"
)

const (
	inactiveMountGcPeriod = time.Minute * 10
)

type mountsMapPerFs map[string]*wekaMount
type mountsMap map[string]mountsMapPerFs

type wekaMounter struct {
	mountMap       mountsMap
	lock           sync.Mutex
	kMounter       mount.Interface
	debugPath      string
	selinuxSupport bool
	gc             *innerPathVolGc
}

func newWekaMounter(driver *WekaFsDriver) *wekaMounter {
	mounter := &wekaMounter{mountMap: mountsMap{}, debugPath: driver.debugPath, selinuxSupport: driver.selinuxSupport}
	mounter.gc = initInnerPathVolumeGc(mounter)
	mounter.schedulePeriodicMountGc()

	return mounter
}

func (m *wekaMounter) NewMount(fsName string, options MountOptions) *wekaMount {
	m.lock.Lock()
	if m.kMounter == nil {
		m.kMounter = mount.New("")
	}
	if _, ok := m.mountMap[fsName]; !ok {
		m.mountMap[fsName] = mountsMapPerFs{}
	}
	if _, ok := m.mountMap[fsName][options.String()]; !ok {
		uniqueId := getStringSha1AsB32(fsName + ":" + options.String())
		wMount := &wekaMount{
			kMounter:     m.kMounter,
			fsName:       fsName,
			debugPath:    m.debugPath,
			mountPoint:   "/run/weka-fs-mounts/" + getAsciiPart(fsName, 64) + "-" + uniqueId,
			mountOptions: options,
		}
		m.mountMap[fsName][options.String()] = wMount
	}
	m.lock.Unlock()
	return m.mountMap[fsName][options.String()]
}

type UnmountFunc func()

func (m *wekaMounter) mountWithOptions(ctx context.Context, fsName string, mountOptions MountOptions, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	mountOptions.setSelinux(m.selinuxSupport)
	mountObj := m.NewMount(fsName, mountOptions)
	mountErr := mountObj.incRef(ctx, apiClient)

	if mountErr != nil {
		log.Ctx(ctx).Error().Err(mountErr).Msg("Failed mounting")
		return "", mountErr, func() {}
	}
	return mountObj.mountPoint, nil, func() {
		if mountErr == nil {
			_ = mountObj.decRef(ctx)
		}
	}
}

func (m *wekaMounter) Mount(ctx context.Context, fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	return m.mountWithOptions(ctx, fs, getDefaultMountOptions(), apiClient)
}

func (m *wekaMounter) unmountWithOptions(ctx context.Context, fsName string, options MountOptions) error {
	opts := options
	options.setSelinux(m.selinuxSupport)

	log.Ctx(ctx).Trace().Strs("mount_options", opts.Strings()).Str("filesystem", fsName).Msg("Received an unmount request")
	if mnt, ok := m.mountMap[fsName][options.String()]; ok {
		err := mnt.decRef(ctx)
		if err == nil {
			if m.mountMap[fsName][options.String()].refCount <= 0 {
				log.Ctx(ctx).Trace().Str("filesystem", fsName).Strs("mount_options", options.Strings()).Msg("This is a last use of this mount, removing from map")
				delete(m.mountMap[fsName], options.String())
			}
			if len(m.mountMap[fsName]) == 0 {
				log.Ctx(ctx).Trace().Str("filesystem", fsName).Msg("No more mounts to filesystem, removing from map")
				delete(m.mountMap, fsName)
			}
		}
		return err

	} else {
		log.Ctx(ctx).Warn().Msg("Attempted to access mount point which is not known to the system")
		return nil
	}
}

func (m *wekaMounter) LogActiveMounts() {
	if len(m.mountMap) > 0 {
		count := 0
		for fsName := range m.mountMap {
			for mnt := range m.mountMap[fsName] {
				mapEntry := m.mountMap[fsName][mnt]
				if mapEntry.refCount > 0 {
					log.Trace().Str("filesystem", fsName).Int("refcount", mapEntry.refCount).Strs("mount_options", mapEntry.mountOptions.Strings()).Msg("Mount is active")
					count++
				} else {
					log.Trace().Str("filesystem", fsName).Int("refcount", mapEntry.refCount).Strs("mount_options", mapEntry.mountOptions.Strings()).Msg("Mount is not active")
				}

			}
		}
		log.Debug().Int("total", len(m.mountMap)).Int("active", count).Msg("Periodic checkup on mount map")
	}
}

func (m *wekaMounter) gcInactiveMounts() {
	if len(m.mountMap) > 0 {
		for fsName := range m.mountMap {
			for uniqueId, wekaMount := range m.mountMap[fsName] {
				if wekaMount.refCount == 0 {
					if wekaMount.lastUsed.Before(time.Now().Add(-inactiveMountGcPeriod)) {
						m.lock.Lock()
						if wekaMount.refCount == 0 {
							log.Trace().Str("filesystem", fsName).Strs("mount_options", wekaMount.mountOptions.Strings()).
								Time("last_used", wekaMount.lastUsed).Msg("Removing stale mount from map")
							delete(m.mountMap[fsName], uniqueId)
						}
						m.lock.Unlock()
					}
				}
			}
			if len(m.mountMap[fsName]) == 0 {
				delete(m.mountMap, fsName)
			}
		}
	}
}

func (m *wekaMounter) schedulePeriodicMountGc() {
	go func() {
		log.Debug().Msg("Initializing periodic mount GC")
		for true {
			m.LogActiveMounts()
			m.gcInactiveMounts()
			time.Sleep(10 * time.Minute)
		}
	}()
}
