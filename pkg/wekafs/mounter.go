package wekafs

import (
	"fmt"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/utils/mount"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type fsMountRequest struct {
	fs    string
	xattr bool
}

type wekaMount struct {
	fsRequest    *fsMountRequest
	mountPoint   string
	refCount     int
	lock         sync.Mutex
	kMounter     mount.Interface
	debugPath    string
	mountOptions []string
}

type mountsMap map[fsMountRequest]*wekaMount

type wekaMounter struct {
	mountMap  mountsMap
	lock      sync.Mutex
	kMounter  mount.Interface
	debugPath string
}

func (m *wekaMount) incRef(apiClient *apiclient.ApiClient) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	if m.refCount < 0 {
		glog.V(4).Infof("During incRef negative refcount encountered, %v", m.refCount)
		m.refCount = 0 // to make sure that we don't have negative refcount later
	}
	if m.refCount == 0 {
		if err := m.doMount(apiClient); err != nil {
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
	glog.V(3).Infof("Calling k8s unmounter for fs: %s (xattr %t) @ %s", m.fsRequest.fs, m.fsRequest.xattr, m.mountPoint)
	err := m.kMounter.Unmount(m.mountPoint)
	if err != nil {
		glog.V(3).Infof("Failed unmounting %s at %s: %s", m.fsRequest.fs, m.mountPoint, err)
	} else {
		glog.V(3).Infof("Successfully unmounted %s (xattr %t) at %s", m.fsRequest.fs, m.fsRequest.xattr, m.mountPoint)
	}
	return err
}

func (m *wekaMount) doMount(apiClient *apiclient.ApiClient) error {
	glog.Infof("Creating mount for filesystem %s on mount point %s", m.fsRequest.fs, m.mountPoint)
	mountToken := ""
	if err := os.MkdirAll(m.mountPoint, DefaultVolumePermissions); err != nil {
		return err
	}
	if m.debugPath == "" {
		mountOptions := getMountOptions(m.fsRequest)
		if apiClient == nil {
			glog.V(3).Infof("No API client for mount, not requesting mount token")
		} else {
			var err error
			glog.V(3).Infof("Requesting mount token for filesystem %s via API", m.fsRequest.fs)
			if mountToken, err = apiClient.GetMountTokenForFilesystemName(m.fsRequest.fs); err != nil {
				return err
			}
			mountOptions = append(mountOptions, fmt.Sprintf("token=%s", mountToken))
		}

		glog.V(3).Infof("Calling k8s mounter for fs: %s (xattr %t) @ %s, authenticated: %s",
			m.fsRequest.fs, m.fsRequest.xattr, m.mountPoint, func() string {
				return strconv.FormatBool(mountToken != "")
			}(),
		)

		return m.kMounter.Mount(m.fsRequest.fs, m.mountPoint, "wekafs", mountOptions)
	} else {
		fakePath := filepath.Join(m.debugPath, m.fsRequest.fs)
		if err := os.MkdirAll(fakePath, DefaultVolumePermissions); err != nil {
			Die(fmt.Sprintf("Failed to create directory %s, while running in debug mode", fakePath))
		}
		glog.V(3).Infof("Calling debugPath k8s mounter for fs: %s (xattr %t) @ %s on fakePath %s", m.fsRequest.fs, m.fsRequest.xattr, m.mountPoint, fakePath)

		return m.kMounter.Mount(fakePath, m.mountPoint, "", []string{"bind"})
	}
}

func getDefaultMountOptions() []string {
	return []string{"writecache"}
}

func getMountOptions(fs *fsMountRequest) []string {
	var mountOptions = getDefaultMountOptions()
	if fs.xattr {
		mountOptions = append(mountOptions, "acl")
	}
	return mountOptions
}

func (m *wekaMounter) initFsMountObject(fs fsMountRequest) {
	m.lock.Lock()
	if m.kMounter == nil {
		m.kMounter = mount.New("")
	}
	if _, ok := m.mountMap[fs]; !ok {
		mountPointUuid, _ := uuid.NewUUID()
		wMount := &wekaMount{
			kMounter:   m.kMounter,
			fsRequest:  &fs,
			debugPath:  m.debugPath,
			mountPoint: "/var/run/weka-fs-mounts/" + getAsciiPart(fs.fs, 64) + "-" + mountPointUuid.String(),
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
		m.mountMap[fs] = wMount
	}
	m.lock.Unlock()
}

type UnmountFunc func()

func (m *wekaMounter) mountParams(fs string, xattr bool, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	request := fsMountRequest{fs, xattr}
	m.initFsMountObject(request)
	mounter := m.mountMap[request]
	mountErr := mounter.incRef(apiClient)

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

func (m *wekaMounter) Mount(fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	m.LogActiveMounts()
	return m.mountParams(fs, false, apiClient)
}

func (m *wekaMounter) MountXattr(fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	m.LogActiveMounts()
	return m.mountParams(fs, true, apiClient)
}

func (m *wekaMounter) Unmount(fs string) error {
	return m.unmount(fs, false)
}

func (m *wekaMounter) UnmountXattr(fs string) error {
	return m.unmount(fs, true)
}

func (m *wekaMounter) unmount(fs string, xattr bool) error {
	m.LogActiveMounts()
	fsReq := fsMountRequest{fs, xattr}
	if mnt, ok := m.mountMap[fsReq]; ok {
		return mnt.decRef()
	} else {
		// TODO: this could happen if the plugin was rebooted with this mount intact. Maybe we might add it to map?
		glog.Warningf("Attempted to access mount point which is not known to the system (filesystem %s)", fs)
		return nil
	}
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
				glog.Infof("Active mount: %s -> %s, xattr: %t, refcount: %d", mnt.fs, mapEntry.mountPoint, mnt.xattr, mapEntry.refCount)
				count++
			} else {
				glog.Infof("Inactive mount: %s", mnt.fs)
			}

		}
		glog.Infof("Total %v of active mounts", count)
	} else {
		glog.Info("There are currently no active mounts")
	}
}
