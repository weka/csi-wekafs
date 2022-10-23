package wekafs

import (
	"context"
	"fmt"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/utils/mount"
	"os"
	"path/filepath"
	"strconv"
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
	m.lock.Lock()
	defer m.lock.Unlock()
	if m.refCount < 0 {
		glog.V(4).Infof("During incRef negative refcount encountered, %v", m.refCount)
		m.refCount = 0 // to make sure that we don't have negative refcount later
	}
	if m.refCount == 0 {
		if err := m.doMount(ctx, apiClient, selinuxSupport); err != nil {
			return err
		}
	}
	m.refCount++
	glog.V(4).Infof("Refcount +1 =  %d @ %s", m.refCount, m.mountPoint)
	return nil
}

func (m *wekaMount) decRef() error {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.refCount--
	m.lastUsed = time.Now()
	glog.V(4).Infof("Refcount -1 =  %d @ %s", m.refCount, m.mountPoint)
	if m.refCount < 0 {
		glog.V(4).Infof("During decRef negative refcount encountered, %v", m.refCount)
		m.refCount = 0 // to make sure that we don't have negative refcount later
	}
	if m.refCount == 0 {
		if err := m.doUnmount(); err != nil {
			return err
		}
	}
	return nil
}

func (m *wekaMount) doUnmount() error {
	glog.V(3).Infof("Calling k8s unmounter for fsName: %s (xattr %t) @ %s", m.fsRequest.fsName, m.fsRequest.xattr, m.mountPoint)
	err := m.kMounter.Unmount(m.mountPoint)
	if err != nil {
		glog.V(3).Infof("Failed unmounting %s at %s: %s", m.fsRequest.fsName, m.mountPoint, err)
	} else {
		glog.V(3).Infof("Successfully unmounted %s (xattr %t) at %s", m.fsRequest.fsName, m.fsRequest.xattr, m.mountPoint)
	}
	return err
}

func (m *wekaMount) doMount(ctx context.Context, apiClient *apiclient.ApiClient, selinuxSupport bool) error {
	glog.Infof("Creating mount for filesystem %s on mount point %s", m.fsRequest.fsName, m.mountPoint)
	mountToken := ""
	var mountOptionsSensitive []string
	if err := os.MkdirAll(m.mountPoint, DefaultVolumePermissions); err != nil {
		return err
	}
	if m.debugPath == "" {
		mountOptions := getMountOptions(m.fsRequest, selinuxSupport)
		if apiClient == nil {
			glog.V(3).Infof("No API client for mount, not requesting mount token")
		} else {
			var err error
			glog.V(3).Infof("Requesting mount token for filesystem %s via API", m.fsRequest.fsName)
			if mountToken, err = apiClient.GetMountTokenForFilesystemName(ctx, m.fsRequest.fsName); err != nil {
				return err
			}
			mountOptionsSensitive = append(mountOptionsSensitive, fmt.Sprintf("token=%s", mountToken))
		}
		glog.V(3).Infof("Calling k8s mounter for fsName: %s (xattr %t) @ %s, options: %s, authenticated: %s",
			m.fsRequest.fsName, m.fsRequest.xattr, m.mountPoint, mountOptions, func() string {
				return strconv.FormatBool(mountToken != "")
			}(),
		)
		return m.kMounter.MountSensitive(m.fsRequest.fsName, m.mountPoint, "wekafs", mountOptions, mountOptionsSensitive)
	} else {
		fakePath := filepath.Join(m.debugPath, m.fsRequest.fsName)
		if err := os.MkdirAll(fakePath, DefaultVolumePermissions); err != nil {
			Die(fmt.Sprintf("Failed to create directory %s, while running in debug mode", fakePath))
		}
		glog.V(3).Infof("Calling debugPath k8s mounter for fsName: %s (xattr %t) @ %s on fakePath %s", m.fsRequest.fsName, m.fsRequest.xattr, m.mountPoint, fakePath)

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
		glog.Errorf("Failed mounting %s at %s: %e", fs, mounter.mountPoint, mountErr)
		return "", mountErr, func() {}
	}
	return mounter.mountPoint, nil, func() {
		if mountErr == nil {
			_ = m.mountMap[request].decRef()
		}
	}
}

func (m *wekaMounter) Mount(ctx context.Context, fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	m.LogActiveMounts()
	return m.mountParams(ctx, fs, false, apiClient)
}

func (m *wekaMounter) MountXattr(ctx context.Context, fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	m.LogActiveMounts()
	return m.mountParams(ctx, fs, true, apiClient)
}

func (m *wekaMounter) Unmount(ctx context.Context, fs string) error {
	defer m.LogActiveMounts()
	return m.unmount(ctx, fs, false)
}

func (m *wekaMounter) UnmountXattr(ctx context.Context, fs string) error {
	defer m.LogActiveMounts()
	return m.unmount(ctx, fs, true)
}

func (m *wekaMounter) unmount(ctx context.Context, fs string, xattr bool) error {
	m.LogActiveMounts()
	defer m.gcInactiveMounts()
	fsReq := fsMountRequest{fs, xattr}
	if mnt, ok := m.mountMap[fsReq]; ok {
		err := mnt.decRef()
		if err != nil {
			if m.mountMap[fsReq].refCount <= 0 {
				glog.V(5).Infoln("This is a last use of this mount, removing from map")
				delete(m.mountMap, fsReq)
			}
		}
		return err

	} else {
		// TODO: this could happen if the plugin was rebooted with this mount intact. Maybe we might add it to map?
		glog.Warningf("Attempted to access mount point which is not known to the system (filesystem %s)", fs)
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
		glog.Infof("There are currently %v distinct mounts in map:", len(m.mountMap))
		for mnt := range m.mountMap {
			mapEntry := m.mountMap[mnt]
			if mapEntry.refCount < 0 {
				glog.Errorf("There is a negative refcount on mount %s", mapEntry.mountPoint)
			} else if mapEntry.refCount > 0 {
				glog.Infof("Active mount: %s -> %s, xattr: %t, refcount: %d", mnt.fsName, mapEntry.mountPoint, mnt.xattr, mapEntry.refCount)
				count++
			} else {
				glog.Infof("Inactive mount: %s, xattr: %t", mnt.fsName, mnt.xattr)
			}

		}
		glog.Infof("Total %v of active mounts", count)
	} else {
		glog.Info("There are currently no active mounts")
	}
}

func (m *wekaMounter) gcInactiveMounts() {
	glog.Infof("Running mount GC")
	for fsRequest, wekaMount := range m.mountMap {
		if wekaMount.refCount == 0 {
			if wekaMount.lastUsed.Before(time.Now().Add(-inactiveMountGcPeriod)) {
				m.lock.Lock()
				if wekaMount.refCount == 0 {
					glog.Infoln("Mount for", fsRequest, "not active since", wekaMount.lastUsed.String(), ", removing from map")
					delete(m.mountMap, fsRequest)
				}
				m.lock.Unlock()
			}
		}
	}
}
