package wekafs

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/utils/mount"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	inactiveMountGcPeriod = time.Minute * 10
)

type fsMountRequest struct {
	fsName string
	xattr  bool
}

type wekaMount struct {
	fsRequest    *fsMountRequest
	mountPoint   string
	refCount     int
	lock         sync.Mutex
	kMounter     mount.Interface
	debugPath    string
	mountOptions []string
	lastUsed     time.Time
}

type mountsMap map[fsMountRequest]*wekaMount

type wekaMounter struct {
	mountMap       mountsMap
	lock           sync.Mutex
	kMounter       mount.Interface
	debugPath      string
	selinuxSupport bool
	gc             *innerPathVolGc
}

func (m *wekaMount) incRef(ctx context.Context, apiClient *apiclient.ApiClient, selinuxSupport bool) error {
	ctx = log.With().Logger().WithContext(ctx)

	m.lock.Lock()
	defer m.lock.Unlock()
	if m.refCount < 0 {
		log.Ctx(ctx).Error().Str("mount_point", m.mountPoint).Int("refcount", m.refCount).Msg("During incRef negative refcount encountered")
		m.refCount = 0 // to make sure that we don't have negative refcount later
	}
	if m.refCount == 0 {
		if err := m.doMount(ctx, apiClient, selinuxSupport); err != nil {
			return err
		}
	}
	m.refCount++
	log.Ctx(ctx).Trace().Int("refcount", m.refCount).Msg("RefCount increased")
	return nil
}

func (m *wekaMount) decRef(ctx context.Context) error {
	logger := log.Ctx(ctx)
	m.lock.Lock()
	defer m.lock.Unlock()
	m.refCount--
	m.lastUsed = time.Now()
	logger.Trace().Int("refcount", m.refCount).Msg("RefCount increased")
	if m.refCount < 0 {
		logger.Error().Int("refcount", m.refCount).Msg("During decRef negative refcount encountered")
		m.refCount = 0 // to make sure that we don't have negative refcount later
	}
	if m.refCount == 0 {
		if err := m.doUnmount(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (m *wekaMount) doUnmount(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.mountPoint).Str("filesystem", m.fsRequest.fsName).Logger()
	logger.Debug().Bool("xattr_flag", m.fsRequest.xattr).Msg("Performing umount via k8s native mounter")
	err := m.kMounter.Unmount(m.mountPoint)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to unmount")
	} else {
		logger.Debug().Msg("Unmounted successfully")
	}
	return err
}

func (m *wekaMount) doMount(ctx context.Context, apiClient *apiclient.ApiClient, selinuxSupport bool) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.mountPoint).Str("filesystem", m.fsRequest.fsName).Logger()
	mountToken := ""
	var mountOptionsSensitive []string
	if err := os.MkdirAll(m.mountPoint, DefaultVolumePermissions); err != nil {
		return err
	}
	if m.debugPath == "" {
		mountOptions := getMountOptions(m.fsRequest, selinuxSupport)
		if apiClient == nil {
			logger.Trace().Msg("No API client for mount, not requesting mount token")
		} else {
			var err error
			logger.Trace().Msg("Requesting mount token via API")
			if mountToken, err = apiClient.GetMountTokenForFilesystemName(ctx, m.fsRequest.fsName); err != nil {
				return err
			}
			mountOptionsSensitive = append(mountOptionsSensitive, fmt.Sprintf("token=%s", mountToken))
		}
		logger.Debug().Bool("xattr_flag", m.fsRequest.xattr).Bool("xattr_flag", m.fsRequest.xattr).
			Fields(mountOptions).Msg("Performing mount")
		return m.kMounter.MountSensitive(m.fsRequest.fsName, m.mountPoint, "wekafs", mountOptions, mountOptionsSensitive)
	} else {
		fakePath := filepath.Join(m.debugPath, m.fsRequest.fsName)
		if err := os.MkdirAll(fakePath, DefaultVolumePermissions); err != nil {
			Die(fmt.Sprintf("Failed to create directory %s, while running in debug mode", fakePath))
		}
		logger.Debug().Bool("xattr_flag", m.fsRequest.xattr).Bool("xattr_flag", m.fsRequest.xattr).
			Str("debug_path", m.debugPath).Msg("Performing mount")

		return m.kMounter.Mount(fakePath, m.mountPoint, "", []string{"bind"})
	}
}

func getDefaultMountOptions() []string {
	return []string{"writecache"}
}

func getMountOptions(fs *fsMountRequest, selinuxSupport bool) []string {
	var mountOptions = getDefaultMountOptions()
	if fs.xattr {
		mountOptions = append(mountOptions, "acl")
	}
	if selinuxSupport {
		mountOptions = append(mountOptions, "fscontext=\"system_u:object_r:wekafs_csi_volume_t:s0\"")
	}
	return mountOptions
}

func (m *wekaMounter) initFsMountObject(fsMountRequest fsMountRequest) {
	m.lock.Lock()
	if m.kMounter == nil {
		m.kMounter = mount.New("")
	}
	if _, ok := m.mountMap[fsMountRequest]; !ok {
		mountPointUuid, _ := uuid.NewUUID()
		wMount := &wekaMount{
			kMounter:   m.kMounter,
			fsRequest:  &fsMountRequest,
			debugPath:  m.debugPath,
			mountPoint: "/var/run/weka-fs-mounts/" + getAsciiPart(fsMountRequest.fsName, 64) + "-" + mountPointUuid.String(),
			// TODO: We might need versioning context, as right now there is no support for different Mount options
			//		 But no need for it now
			//		 In case we reach there - worst case we get dangling mounts
			//		 And we can detect dangling mounts just by doing
			//		 Unmount on non-registered mounts inside main mounts dir
			//		 In general..this might need more thinking, but getting to working version ASAP is a priority
			//       Even without version/Mount options change - plugin restart will lead to dangling mounts
			//       We also might use VolumeContext to save it's parent FS Mount path instead of calculating
			mountOptions: getDefaultMountOptions(),
		}
		m.mountMap[fsMountRequest] = wMount
	}
	m.lock.Unlock()
}

type UnmountFunc func()

func (m *wekaMounter) mountParams(ctx context.Context, fs string, xattr bool, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	request := fsMountRequest{fs, xattr}
	m.initFsMountObject(request)
	mounter := m.mountMap[request]
	mountErr := mounter.incRef(ctx, apiClient, m.selinuxSupport)

	if mountErr != nil {
		log.Ctx(ctx).Error().Err(mountErr).Msg("Failed mounting")
		return "", mountErr, func() {}
	}
	return mounter.mountPoint, nil, func() {
		if mountErr == nil {
			_ = m.mountMap[request].decRef(ctx)
		}
	}
}

func (m *wekaMounter) Mount(ctx context.Context, fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	return m.mountParams(ctx, fs, false, apiClient)
}

func (m *wekaMounter) MountXattr(ctx context.Context, fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	return m.mountParams(ctx, fs, true, apiClient)
}

func (m *wekaMounter) Unmount(ctx context.Context, fs string) error {
	return m.unmount(ctx, fs, false)
}

func (m *wekaMounter) UnmountXattr(ctx context.Context, fs string) error {
	return m.unmount(ctx, fs, true)
}

func (m *wekaMounter) unmount(ctx context.Context, fs string, xattr bool) error {
	fsReq := fsMountRequest{fs, xattr}
	if mnt, ok := m.mountMap[fsReq]; ok {
		err := mnt.decRef(ctx)
		if err != nil {
			if m.mountMap[fsReq].refCount <= 0 {
				log.Ctx(ctx).Trace().Str("filesystem", fsReq.fsName).Bool("xattr_flag", fsReq.xattr).Msg("This is a last use of this mount, removing from map")
				delete(m.mountMap, fsReq)
			}
		}
		return err

	} else {
		// TODO: this could happen if the plugin was rebooted with this mount intact. Maybe we might add it to map?
		log.Ctx(ctx).Warn().Msg("Attempted to access mount point which is not known to the system")
		return nil
	}
}

func (m *wekaMounter) HasMount(filesystem string, xattr bool) bool {
	fsReq := fsMountRequest{filesystem, xattr}
	if mnt, ok := m.mountMap[fsReq]; ok {
		return mnt.refCount > 0
	}
	return false
}

func (m *wekaMounter) LogActiveMounts() {
	if len(m.mountMap) > 0 {
		count := 0
		for mnt := range m.mountMap {
			mapEntry := m.mountMap[mnt]
			if mapEntry.refCount > 0 {
				log.Trace().Str("filesystem", mnt.fsName).Int("refcount", mapEntry.refCount).Bool("xattr_flag", mnt.xattr).
					Msg("Mount is active")
				count++
			} else {
				log.Trace().Str("filesystem", mnt.fsName).Int("refcount", mapEntry.refCount).Bool("xattr_flag", mnt.xattr).Msg("Mount is not active")
			}

		}
		log.Debug().Int("total", len(m.mountMap)).Int("active", count).Msg("Periodic checkup on mount map")
	}
}

func (m *wekaMounter) gcInactiveMounts() {
	if len(m.mountMap) > 0 {
		log.Trace().Msg("Running mount GC")
		for fsRequest, wekaMount := range m.mountMap {
			if wekaMount.refCount == 0 {
				if wekaMount.lastUsed.Before(time.Now().Add(-inactiveMountGcPeriod)) {
					m.lock.Lock()
					if wekaMount.refCount == 0 {
						log.Trace().Str("filesystem", fsRequest.fsName).Bool("xattr_flag", fsRequest.xattr).Time("last_used", wekaMount.lastUsed).Msg("Removing stale moung from map")
						delete(m.mountMap, fsRequest)
					}
					m.lock.Unlock()
				}
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
			time.Sleep(1 * time.Minute)
		}
	}()
}
