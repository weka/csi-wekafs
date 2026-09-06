package wekafs

import (
	"context"
	"path"
	"sync"
	"sync/atomic"
	"testing"
)

// Unmounting picks its mounter from the mount path, so this mapping is what keeps an NFS mount from
// being handed to the wekafs mounter once the preferred transport moves. The dev implementation this
// was ported from returned wekafs from every branch, including the NFS one, which silently defeated
// the fix it was written for - hence the explicit case per transport.
func TestGetDataTransportFromMountPath(t *testing.T) {
	for name, tc := range map[string]struct {
		mountPath string
		want      DataTransport
	}{
		"nfs mount under the node base": {
			mountBaseDirForRole(CsiModeNode, dataTransportNfs) + "/default-abc123", dataTransportNfs,
		},
		"wekafs mount under the node base": {
			mountBaseDirForRole(CsiModeNode, dataTransportWekafs) + "/default-abc123", dataTransportWekafs,
		},
		"nfs mount under the controller base": {
			mountBaseDirForRole(CsiModeController, dataTransportNfs) + "/default-abc123", dataTransportNfs,
		},
		"wekafs mount under the metrics-server (default) base": {
			mountBaseDirForRole(CsiModeMetricsServer, dataTransportWekafs) + "/default-abc123", dataTransportWekafs,
		},
		// A filesystem may legitimately be called "nfs"; only the path segment decides.
		"filesystem named nfs on a wekafs mount": {
			path.Join(mountBaseDirForRole(CsiModeNode, dataTransportWekafs), "nfs-abc123"), dataTransportWekafs,
		},
		"filesystem with an nfs prefix": {
			path.Join(mountBaseDirForRole(CsiModeNode, dataTransportWekafs), "nfsdata-abc123"), dataTransportWekafs,
		},
		// Anything unrecognised, including a mount made before transports had their own directories.
		"legacy path without a transport segment": {"/run/weka-fs-mounts-node/default-abc123", dataTransportWekafs},
		"empty path": {"", dataTransportWekafs},
	} {
		if got := getDataTransportFromMountPath(tc.mountPath); got != tc.want {
			t.Errorf("%s: getDataTransportFromMountPath(%q) = %q, want %q", name, tc.mountPath, got, tc.want)
		}
	}
}

// Each transport must get its own directory, or two mounts of the same filesystem could collide:
// both mounters name a mount {fsName}-{sha1(fsName:options)}, so identical option strings would
// otherwise produce identical paths and unmounting one would tear down the other.
func TestMountBaseDirsAreDisjointPerTransport(t *testing.T) {
	for _, mode := range []CsiPluginMode{CsiModeNode, CsiModeController} {
		nfs := mountBaseDirForRole(mode, dataTransportNfs)
		wekafs := mountBaseDirForRole(mode, dataTransportWekafs)
		if nfs == wekafs {
			t.Errorf("%s: both transports share the base dir %q", mode, nfs)
		}
	}

	// Roles must stay disjoint too - a controller and a node on the same host each mount their own.
	if mountBaseDirForRole(CsiModeNode, dataTransportWekafs) == mountBaseDirForRole(CsiModeController, dataTransportWekafs) {
		t.Error("node and controller share a mount base dir")
	}
}

// GetMounterByTransport must return a disabled mounter, since unmounting a volume mounted before a
// failback has to reach the mounter that made it. GetPreferredMounter must not.
func TestMounterGroupSelection(t *testing.T) {
	ctx := context.Background()
	nfs := &nfsMounter{}
	wekafs := &wekafsMounter{}
	mg := &MounterGroup{nfs: nfs, wekafs: wekafs}

	nfs.Disable()
	wekafs.Disable()
	if got := mg.GetMounterByTransport(ctx, dataTransportNfs); got != AnyMounter(nfs) {
		t.Error("GetMounterByTransport did not return the disabled NFS mounter - a mount made over it could not be unmounted")
	}
	if got := mg.GetPreferredMounter(ctx); got != nil {
		t.Errorf("GetPreferredMounter returned %v with every transport disabled, want nil", got)
	}

	// Preference order is wekafs first, so NFS is only chosen when wekafs is unavailable.
	nfs.Enable()
	if got := mg.GetPreferredMounter(ctx); got != AnyMounter(nfs) {
		t.Error("GetPreferredMounter did not fall back to NFS when wekafs was disabled")
	}
	wekafs.Enable()
	if got := mg.GetPreferredMounter(ctx); got != AnyMounter(wekafs) {
		t.Error("GetPreferredMounter did not prefer wekafs when both were enabled")
	}

	if got := mg.GetMounterByTransport(ctx, DataTransport("carrier-pigeon")); got != nil {
		t.Errorf("GetMounterByTransport returned %v for an unknown transport, want nil", got)
	}
}

// The refcount map is the one piece of mount bookkeeping shared by both transports, and it is locked
// per entry rather than per mounter. Two filesystems must be able to mount at once; two callers of
// the same mount must not.
func TestMountMapLocksPerEntry(t *testing.T) {
	mm := newMountMap()

	if lockA, lockB := mm.lockFor("/mnt/a^opts"), mm.lockFor("/mnt/b^opts"); lockA == lockB {
		t.Fatal("different mounts share a lock - mounting one filesystem would block mounting another")
	}
	if mm.lockFor("/mnt/a^opts") != mm.lockFor("/mnt/a^opts") {
		t.Error("the same mount handed out two different locks - two callers could mount concurrently")
	}

	// A fresh entry starts at zero, which is what makes the first caller perform the mount.
	_ = mm.WithEntry("/mnt/a^opts", func(c *atomic.Int32) error {
		if c.Load() != 0 {
			t.Errorf("new entry started at %d, want 0", c.Load())
		}
		c.Add(1)
		return nil
	})
	_ = mm.WithEntry("/mnt/a^opts", func(c *atomic.Int32) error {
		if c.Load() != 1 {
			t.Errorf("refcount did not survive reload: got %d, want 1 - each caller would see its own count", c.Load())
		}
		return nil
	})

	// Load must not create, or a sweep would resurrect the entries it is pruning.
	if _, _, ok := mm.Load("/mnt/never-seen^opts"); ok {
		t.Error("Load reported an entry that was never stored")
	}
	if mm.Len() != 1 {
		t.Errorf("Len() = %d after loading an absent key, want 1 - Load created an entry", mm.Len())
	}

	mm.Prune("/mnt/a^opts")
	if _, _, ok := mm.Load("/mnt/a^opts"); ok {
		t.Error("pruned entry still present")
	}
}

// The sweep prunes zero-refcount entries while callers are taking references. If a caller could hold
// a counter that the sweep has already dropped from the map, two callers would count the same mount
// separately and whichever reached zero first would unmount it underneath the other. WithEntry
// resolves the counter under the entry's lock precisely to stop that, and -race is what proves it.
func TestMountMapConcurrentRefcountingSurvivesPruning(t *testing.T) {
	mm := newMountMap()
	const refIndex = "/mnt/contended^opts"
	const callers = 32

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Take a reference and drop it again, as a mount and unmount pair would.
			_ = mm.WithEntry(refIndex, func(c *atomic.Int32) error { c.Add(1); return nil })
			_ = mm.WithEntry(refIndex, func(c *atomic.Int32) error { c.Add(-1); return nil })
		}()
	}
	// A sweep running throughout, pruning whenever it observes zero - exactly the interleaving that
	// used to hand a later caller a second, disconnected counter.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < callers*4; i++ {
			for _, idx := range mm.Indexes() {
				if refCount, lock, ok := mm.Load(idx); ok {
					lock.Lock()
					if refCount.Load() == 0 {
						mm.Prune(idx)
					}
					lock.Unlock()
				}
			}
		}
	}()
	wg.Wait()

	// Every caller released what it took, so nothing may be left holding a reference.
	for _, idx := range mm.Indexes() {
		if refCount, _, ok := mm.Load(idx); ok && refCount.Load() != 0 {
			t.Errorf("%s ended at refcount %d, want 0 - references were counted on separate counters", idx, refCount.Load())
		}
	}
}

// -selinux-support forces SELinux on without probing for it. The flag is seeded at construction, so
// a regression here is silent: detection simply runs instead and the forced setting is ignored.
func TestForcedSelinuxSkipsDetection(t *testing.T) {
	ctx := context.Background()

	var forced mounterState
	forced.forceSelinux()
	if !forced.getSelinuxStatus(ctx) {
		t.Error("forceSelinux did not take effect - the -selinux-support flag would be ignored")
	}

	// Without the flag the cache starts empty, so detection is what answers.
	var probed mounterState
	if probed.selinuxSupport != nil {
		t.Error("selinux status was cached before anything probed for it")
	}
}

// Both mounters must expose the shared lifecycle, since Probe drives them through AnyMounter.
func TestMountersImplementSharedLifecycle(t *testing.T) {
	for name, m := range map[string]AnyMounter{"nfs": &nfsMounter{}, "wekafs": &wekafsMounter{}} {
		if m.isEnabled() {
			t.Errorf("%s: enabled before anything enabled it", name)
		}
		m.Enable()
		if !m.isEnabled() {
			t.Errorf("%s: Enable did not take", name)
		}
		m.Disable()
		if m.isEnabled() {
			t.Errorf("%s: Disable did not take", name)
		}
	}
}
