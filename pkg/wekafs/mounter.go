package wekafs

import (
	"github.com/golang/glog"
	"github.com/google/uuid"
	"k8s.io/kubernetes/pkg/util/mount"
	"os"
	"path/filepath"
	"sync"
)

const debugMode = true

type fsRequest struct {
	fs    string
	xattr bool
}

type wekaMount struct {
	fs         string
	mountPoint string
	refCount   int
	lock       sync.Mutex
	kMounter   mount.Interface
}

type wekaMounter struct {
	mountMap map[fsRequest]*wekaMount
	lock     sync.Mutex
	kMounter mount.Interface
}

func (m *wekaMount) incRef() error {
	m.lock.Lock()
	defer m.lock.Unlock()
	if m.refCount == 0 {
		if err := m.doMount(); err != nil {
			return err
		}
		m.refCount++
	}
	return nil
}

func (m *wekaMount) decRef() error {
	m.lock.Lock()
	m.refCount--
	if m.refCount <= 0 {
		if err := m.doUnmount(); err != nil {
			return err
		}
	}
	m.lock.Unlock()
	return nil
}

func (m *wekaMount) doUnmount() error {
	return m.kMounter.Unmount(m.mountPoint)
}

func (m *wekaMount) doMount() error {
	if err := os.MkdirAll(m.mountPoint, 0750); err != nil {
		return err
	}
	if !debugMode {
		return m.kMounter.Mount(m.fs, m.mountPoint, "wekafs", []string{})
	} else {
		fakePath := filepath.Join("/tmp/csi-wekafs-fakemounts", m.fs)
		if err := os.MkdirAll(fakePath, 0750); err != nil {
			panic("Failed to create directory")
		}

		return m.kMounter.Mount(fakePath, m.mountPoint, "", []string{"bind"})
	}
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
		mount := &wekaMount{
			kMounter:   m.kMounter,
			fs:         fs.fs,
			mountPoint: "/var/run/weka-mounts/" + getAsciiPart(fs.fs, 64) + "-" + mountPointUuid.String(),
			// TODO: We might need versioning context, as right now there is no support for different Mount options
			//		 But no need for it now
			//		 In case we reach there - worst case we get dangling mounts
			//		 And we can detect dangling mounts just by doing
			//		 Unmount on non-registered mounts inside main mounts dir
			//		 In general..this might need more thinking, but getting to working version ASAP is a priority
			//       Even without version/Mount options change - plugin restart will lead to dangling mounts
			//       We also might use VolumeContext to save it's parent FS Mount path instead of calculating
		}
		m.mountMap[fs] = mount
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
		return "", mountErr, func() {}
	}
	return mounter.mountPoint, nil, func() {
		if mountErr == nil {
			if err := m.mountMap[request].decRef(); err != nil {
				glog.V(3).Info("Failed unmounting %s at %s", fs, mounter.mountPoint)
			}
		}
	}
}

func (m *wekaMounter) Mount(fs string) (string, error, UnmountFunc) {
	return m.mountParams(fs, false)
}

func (m *wekaMounter) MountXattr(fs string) (string, error, UnmountFunc) {
	return m.mountParams(fs, true)
}

func (m *wekaMounter) Unmount(fs string) error {
	return m.mountMap[fsRequest{fs, false}].decRef()
}

func (m *wekaMounter) UnmountXattr(fs string) error {
	return m.mountMap[fsRequest{fs, true}].decRef()
}
