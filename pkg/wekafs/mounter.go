package wekafs

import (
	"context"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/utils/mount"
	"os"
	"path"
	"strconv"
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
	if mounter.debugPath == "" {
		if err := mounter.recoverExistingMounts(); err != nil {
			log.Warn().Msg("Failed to recover existing mounts")
		}
	}
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
			mountPoint:   "/var/run/weka-fs-mounts/" + getAsciiPart(fsName, 64) + "-" + uniqueId,
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

	if mountOptions.hasOption(MountOptionSyncOnClose) && (apiClient == nil || !apiClient.SupportsSyncOnCloseMountOption()) {
		logger := log.Ctx(ctx)
		logger.Debug().Str("mount_option", MountOptionSyncOnClose).Msg("Mount option not supported by current Weka cluster version and is dropped.")
		mountOptions = mountOptions.RemoveOption(MountOptionSyncOnClose)
	}

	mounter := m.NewMount(fsName, mountOptions)
	mountErr := mounter.incRef(ctx, apiClient)

	if mountErr != nil {
		log.Ctx(ctx).Error().Err(mountErr).Msg("Failed mounting")
		return "", mountErr, func() {}
	}
	return mounter.mountPoint, nil, func() {
		if mountErr == nil {
			_ = mounter.decRef(ctx)
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
			if len(m.mountMap[fsName]) < 1 {
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

// recoverExistingMounts rebuilds mounts that were lost due to pod restart
func (m *wekaMounter) recoverExistingMounts() error {
	// if the CSI pod is restarted, the mounterMap is reset, but the existing mounts still show in /proc/PID/mountinfo in the following format (only the wekafs mounts are relevant)
	//961 1050 0:16625 / /run/weka-fs-mounts/csivol-pvc-5580031e-MRECGMUDSRWQ-bee897f6-a068-11ed-a831-0a613658bb69 rw,relatime - wekafs csivol-pvc-5580031e-MRECGMUDSRWQ rw,writecache,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0
	//1110 1106 0:16625 /.snapshots/pvc-376dc1ee-NFYRYCJR4SBJ /var/lib/kubelet/pods/0ec9fa65-0b28-4b6b-8fd8-32242f4e9e44/volumes/kubernetes.io~csi/pvc-376dc1ee-c781-41bb-a20a-3fb16919280a/mount rw,relatime shared:387 - wekafs csivol-pvc-5580031e-MRECGMUDSRWQ rw,writecache,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0
	//962 1106 0:16625 / /var/lib/kubelet/pods/6eb80ba2-f11a-4a87-9f3d-81cd5bfaf65a/volumes/kubernetes.io~csi/pvc-5580031e-ff1b-44be-b4aa-05250fbc7009/mount rw,relatime shared:417 - wekafs csivol-pvc-5580031e-MRECGMUDSRWQ rw,writecache,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0
	//1287 1106 0:16625 /.snapshots/pvc-402496f8-4A3DOEN5RYWL /var/lib/kubelet/pods/4b6be187-e00e-4f8c-8619-36f5760cb9c9/volumes/kubernetes.io~csi/pvc-402496f8-96f0-4c30-a442-a7eba0610e5e/mount rw,relatime shared:447 - wekafs csivol-pvc-5580031e-MRECGMUDSRWQ rw,writecache,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0
	//
	// There are 2 types of mounts:
	// - those in /run/weka-fs-mounts (961 above): the actual mounts to wekafs filesystems, always are to filesystem root. They do not survive pod reboot
	// - those having a root or optional inner path on mountPoint and /var/lib/kubelet/pods/<pod>/volumes/.... on target are bind mounts - the num of references to the mounted FS.
	// So we need to first build a map of existing mounts based on the /run/weka-fs-mounts, and repopulate with mountOptions
	// then, for each filesystem, we need to increase refCounts when mountOpts are the same
	logger := log.Logger

	pid := os.Getpid()
	mountInfoPath := path.Join("/proc", strconv.Itoa(pid), "mountinfo")
	logger.Debug().Str("mount_info_path", mountInfoPath).Msg("Recovering existing mounts")
	allMounts, err := mount.ParseMountInfo(mountInfoPath)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to recover existing mounts, could not parse mountinfo")
		return err
	}
	for _, mi := range allMounts {
		if mi.FsType != "wekafs" {
			// skip all irrelevant mounts (tmpfs, secreta etc.)
			continue
		}

		logger.Info().
			Str("Root", mi.Root).
			Str("Source", mi.Source).
			Str("MountPoint", mi.MountPoint).
			Strs("MountOptions", mi.MountOptions).
			Strs("SuperOptions", mi.SuperOptions).Msg("Recovering existing mount")

		mOpts := NewMountOptions(mi.SuperOptions)

		mounter := m.NewMount(mi.Source, mOpts)
		mounter.refCount += 1
		mounter.lastUsed = time.Now()

	}
	m.LogActiveMounts()
	return nil
}
