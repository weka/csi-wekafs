package wekafs

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/mount-utils"
	"sync"
	"time"
)

type nfsMounter struct {
	mountMap              nfsMountsMap
	lock                  sync.Mutex
	kMounter              mount.Interface
	debugPath             string
	selinuxSupport        *bool
	gc                    *innerPathVolGc
	interfaceGroupName    *string
	clientGroupName       string
	nfsProtocolVersion    string
	exclusiveMountOptions []mutuallyExclusiveMountOptionSet
}

func (m *nfsMounter) getGarbageCollector() *innerPathVolGc {
	return m.gc
}

func newNfsMounter(driver *WekaFsDriver) *nfsMounter {
	var selinuxSupport *bool
	if driver.selinuxSupport {
		log.Debug().Msg("SELinux support is forced")
		selinuxSupport = &[]bool{true}[0]
	}
	mounter := &nfsMounter{mountMap: make(nfsMountsMap), debugPath: driver.debugPath, selinuxSupport: selinuxSupport, exclusiveMountOptions: driver.config.mutuallyExclusiveOptions}
	mounter.gc = initInnerPathVolumeGc(mounter)
	mounter.schedulePeriodicMountGc()
	mounter.interfaceGroupName = &driver.config.interfaceGroupName
	mounter.clientGroupName = driver.config.clientGroupName
	mounter.nfsProtocolVersion = driver.config.nfsProtocolVersion

	return mounter
}

func (m *nfsMounter) NewMount(fsName string, options MountOptions) AnyMount {
	if m.kMounter == nil {
		m.kMounter = mount.New("")
	}
	uniqueId := getStringSha1AsB32(fsName + ":" + options.String())
	wMount := &nfsMount{
		mounter:            m,
		kMounter:           m.kMounter,
		fsName:             fsName,
		debugPath:          m.debugPath,
		mountPoint:         "/run/weka-fs-mounts/" + getAsciiPart(fsName, 64) + "-" + uniqueId,
		mountOptions:       options,
		interfaceGroupName: m.interfaceGroupName,
		clientGroupName:    m.clientGroupName,
		protocolVersion:    apiclient.NfsVersionString(fmt.Sprintf("V%s", m.nfsProtocolVersion)),
	}
	return wMount
}

func (m *nfsMounter) getSelinuxStatus(ctx context.Context) bool {
	if m.selinuxSupport != nil && *m.selinuxSupport {
		return true
	}
	selinuxSupport := getSelinuxStatus(ctx)
	m.selinuxSupport = &selinuxSupport
	return *m.selinuxSupport
}

func (m *nfsMounter) mountWithOptions(ctx context.Context, fsName string, mountOptions MountOptions, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	mountOptions.setSelinux(m.getSelinuxStatus(ctx), MountProtocolNfs)
	mountOptions = mountOptions.AsNfs()
	mountOptions.Merge(mountOptions, m.exclusiveMountOptions)
	mountObj := m.NewMount(fsName, mountOptions)
	mountErr := mountObj.incRef(ctx, apiClient)

	if mountErr != nil {
		log.Ctx(ctx).Error().Err(mountErr).Msg("Failed mounting")
		return "", mountErr, func() {}
	}
	return mountObj.getMountPoint(), nil, func() {
		if mountErr == nil {
			_ = mountObj.decRef(ctx)
		}
	}
}

func (m *nfsMounter) Mount(ctx context.Context, fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	return m.mountWithOptions(ctx, fs, getDefaultMountOptions(), apiClient)
}

func (m *nfsMounter) unmountWithOptions(ctx context.Context, fsName string, options MountOptions) error {
	opts := options
	options.setSelinux(m.getSelinuxStatus(ctx), MountProtocolNfs)
	options = options.AsNfs()
	options.Merge(options, m.exclusiveMountOptions)
	mnt := m.NewMount(fsName, options)
	// since we are not aware of the IP address of the mount, we need to find the mount point by listing the mounts
	err := mnt.locateMountIP()
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("Failed to locate mount IP")
		return err
	}

	log.Ctx(ctx).Trace().Strs("mount_options", opts.Strings()).Str("filesystem", fsName).Msg("Received an unmount request")
	return mnt.decRef(ctx)
}

func (m *nfsMounter) LogActiveMounts() {
	//if len(m.mountMap) > 0 {
	//	count := 0
	//	for fsName := range m.mountMap {
	//		for mnt := range m.mountMap[fsName] {
	//			mapEntry := m.mountMap[fsName][mnt]
	//			if mapEntry.getRefCount() > 0 {
	//				log.Trace().Str("filesystem", fsName).Int("refcount", mapEntry.getRefCount()).Strs("mount_options", mapEntry.getMountOptions().Strings()).Msg("Mount is active")
	//				count++
	//			} else {
	//				log.Trace().Str("filesystem", fsName).Int("refcount", mapEntry.getRefCount()).Strs("mount_options", mapEntry.getMountOptions().Strings()).Msg("Mount is not active")
	//			}
	//
	//		}
	//	}
	//	log.Debug().Int("total", len(m.mountMap)).Int("active", count).Msg("Periodic checkup on mount map")
	//}
}

func (m *nfsMounter) gcInactiveMounts() {
	//if len(m.mountMap) > 0 {
	//	for fsName := range m.mountMap {
	//		for uniqueId, wekaMount := range m.mountMap[fsName] {
	//			if wekaMount.getRefCount() == 0 {
	//				if wekaMount.getLastUsed().Before(time.Now().Add(-inactiveMountGcPeriod)) {
	//					m.lock.Lock()
	//					if wekaMount.getRefCount() == 0 {
	//						log.Trace().Str("filesystem", fsName).Strs("mount_options", wekaMount.getMountOptions().Strings()).
	//							Time("last_used", wekaMount.getLastUsed()).Msg("Removing stale mount from map")
	//						delete(m.mountMap[fsName], uniqueId)
	//					}
	//					m.lock.Unlock()
	//				}
	//			}
	//		}
	//		if len(m.mountMap[fsName]) == 0 {
	//			delete(m.mountMap, fsName)
	//		}
	//	}
	//}
}

func (m *nfsMounter) schedulePeriodicMountGc() {
	go func() {
		log.Debug().Msg("Initializing periodic mount GC")
		for true {
			m.LogActiveMounts()
			m.gcInactiveMounts()
			time.Sleep(10 * time.Minute)
		}
	}()
}

func (m *nfsMounter) getTransport() DataTransport {
	return dataTransportNfs
}
