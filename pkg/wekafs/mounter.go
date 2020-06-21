package wekafs

import (
	"github.com/golang/glog"
	"github.com/google/uuid"
	"k8s.io/utils/mount"
	"os"
	"path/filepath"
	"sync"
)

type fsRequest struct {
	fs    string
	xattr bool
}

type wekaMount struct {
	fsRequest    *fsRequest
	mountPoint   string
	refCount     int
	lock         sync.Mutex
	kMounter     mount.Interface
	debugPath    string
	mountOptions []string
}

type mountsMap map[fsRequest]*wekaMount

type wekaMounter struct {
	mountMap  mountsMap
	lock      sync.Mutex
	kMounter  mount.Interface
	debugPath string
}

func (m *wekaMount) incRef() error {
	m.lock.Lock()
	defer m.lock.Unlock()
	if m.refCount == 0 {
		if err := m.doMount(); err != nil {
			return err
		}
	}
	m.refCount++
	glog.V(7).Infof("Refcount +1 =  %d @ %s", m.refCount, m.mountPoint)
	return nil
}

func (m *wekaMount) decRef() error {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.refCount--
	glog.V(7).Infof("Refcount -1 =  %d @ %s", m.refCount, m.mountPoint)
	if m.refCount <= 0 {
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
		glog.V(3).Infof("Successfully unmounted %s (xattr %t) at %s: %s", m.fsRequest.fs, m.fsRequest.xattr, m.mountPoint, err)
	}
	return err
}

func (m *wekaMount) doMount() error {
	glog.Infof("Creating mount for filesystem %s on mount point %s", m.fsRequest.fs, m.mountPoint)
	if err := os.MkdirAll(m.mountPoint, 0750); err != nil {
		return err
	}
	if m.debugPath == "" {
		glog.V(3).Infof("Calling k8s mounter for fs: %s (xattr %t) @ %s", m.fsRequest.fs, m.fsRequest.xattr, m.mountPoint)
		return m.kMounter.Mount(m.fsRequest.fs, m.mountPoint, "wekafs", getMountOptions(m.fsRequest))
	} else {
		fakePath := filepath.Join(m.debugPath, m.fsRequest.fs)
		if err := os.MkdirAll(fakePath, 0750); err != nil {
			panic("Failed to create directory")
		}
		glog.V(3).Infof("Calling debugPath k8s mounter for fs: %s (xattr %t) @ %s on fakePath %s", m.fsRequest.fs, m.fsRequest.xattr, m.mountPoint, fakePath)

		return m.kMounter.Mount(fakePath, m.mountPoint, "", []string{"bind"})
	}
}

func getMountOptions(fs *fsRequest) []string {
	var mountOptions []string
	if fs.xattr {
		mountOptions = append(mountOptions, "acl")
	}
	return mountOptions
}

func (m *wekaMounter) initFsMountObject(fs fsRequest) {
	m.lock.Lock()
	if m.kMounter == nil {
		m.kMounter = mount.New("")
	}
	if _, ok := m.mountMap[fs]; !ok {
		mountPointUuid, err := uuid.NewUUID()
		if err != nil {
			panic(err)
		}
		var mountOptions []string
		if fs.xattr == true {
			mountOptions = append(mountOptions, "acl")

		}
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
			mountOptions: mountOptions,
		}
		m.mountMap[fs] = wMount
	}
	m.lock.Unlock()
}

type UnmountFunc func()

func (m *wekaMounter) mountParams(fs string, xattr bool) (string, error, UnmountFunc) {
	request := fsRequest{fs, xattr}
	m.initFsMountObject(request)
	mounter := m.mountMap[request]
	mountErr := mounter.incRef()

	if mountErr != nil {
		glog.Errorf("Failed mounting %s at %s: %e", fs, mounter.mountPoint, mounter)
		return "", mountErr, func() {}
	}
	return mounter.mountPoint, nil, func() {
		if mountErr == nil {
			_ = m.mountMap[request].decRef()
		}
	}
}

func (m *wekaMounter) Mount(fs string) (string, error, UnmountFunc) {
	m.LogActiveMounts()
	return m.mountParams(fs, false)
}

func (m *wekaMounter) MountXattr(fs string) (string, error, UnmountFunc) {
	m.LogActiveMounts()
	return m.mountParams(fs, true)
}

func (m *wekaMounter) Unmount(fs string) error {
	m.LogActiveMounts()
	return m.mountMap[fsRequest{fs, false}].decRef()
}

func (m *wekaMounter) UnmountXattr(fs string) error {
	m.LogActiveMounts()
	return m.mountMap[fsRequest{fs, true}].decRef()
}

func (m *wekaMounter) LogActiveMounts() {
	if len(m.mountMap) > 0 {
		count := 0
		glog.Infof("There are currently %v distinct mounts in map:", len(m.mountMap))
		for mnt := range m.mountMap {
			mapEntry := m.mountMap[mnt]
			if mapEntry.refCount < 0 {
				glog.Errorf("There is a negative refcount on mount %s", mapEntry)
			} else if mapEntry.refCount > 0 {
				glog.Infof("Active mount: %s -> %s, xattr: %s", mnt.fs, mapEntry.mountPoint, mnt.xattr)
				count++
			}
		}
		glog.Infof("Total %v of active mounts", count)
	} else {
		glog.Info("There are currently no active mounts")
	}
}
