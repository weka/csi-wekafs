package wekafs

import (
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/types"
)

func TestVolumeMetricsAddRemove(t *testing.T) {
	vms := NewVolumeMetrics()
	fs := uuid.New()
	key := volumeKey{filesystemUid: fs, inodeId: 42}
	vm := &VolumeMetric{}

	if vms.Len() != 0 || vms.Has("pv-1") {
		t.Fatal("a new VolumeMetrics is not empty")
	}

	vms.Add("pv-1", key, vm)
	if !vms.Has("pv-1") || vms.Get("pv-1") != vm || vms.Len() != 1 {
		t.Fatal("volume was not tracked by PV uid")
	}
	if got := vms.ForInode(fs, 42); len(got) != 1 || got[0] != vm {
		t.Fatalf("ForInode returned %v, want the tracked volume", got)
	}
	if got := vms.ForFilesystem(fs); len(got) != 1 || got[0] != vm {
		t.Fatalf("ForFilesystem returned %v, want one volume", got)
	}

	vms.Remove("pv-1")
	if vms.Has("pv-1") || vms.Len() != 0 {
		t.Fatal("volume was not removed from the PV index")
	}
	if got := vms.ForInode(fs, 42); len(got) != 0 {
		t.Fatalf("ForInode still returns %v after removal", got)
	}
	if got := vms.ForFilesystem(fs); len(got) != 0 {
		t.Fatalf("ForFilesystem still returns %v after removal", got)
	}
}

// A volume whose filesystem or inode is unknown is still tracked by PV - it can report the capacity
// Kubernetes knows - it just cannot be matched to a quota. The version this was ported from panicked
// instead, which would have taken the metrics server down over one unresolvable volume.
func TestVolumeMetricsAddWithoutInode(t *testing.T) {
	vms := NewVolumeMetrics()
	vms.Add("pv-1", volumeKey{}, &VolumeMetric{})
	if !vms.Has("pv-1") {
		t.Error("volume with no filesystem or inode was not tracked")
	}
	if got := vms.ForFilesystem(uuid.Nil); len(got) != 0 {
		t.Errorf("volume with no filesystem leaked into the by-filesystem index: %v", got)
	}
	vms.Remove("pv-1") // must not panic
}

// Re-adding the same PV at a different location must not leave the old index entry behind.
func TestVolumeMetricsReAddMoves(t *testing.T) {
	vms := NewVolumeMetrics()
	oldFs, newFs := uuid.New(), uuid.New()
	vms.Add("pv-1", volumeKey{filesystemUid: oldFs, inodeId: 1}, &VolumeMetric{})
	vm2 := &VolumeMetric{}
	vms.Add("pv-1", volumeKey{filesystemUid: newFs, inodeId: 2}, vm2)

	if got := vms.ForInode(oldFs, 1); len(got) != 0 {
		t.Errorf("stale index entry left at the old location: %v", got)
	}
	if got := vms.ForInode(newFs, 2); len(got) != 1 || got[0] != vm2 {
		t.Errorf("ForInode at the new location returned %v, want the new volume", got)
	}
	if vms.Len() != 1 {
		t.Errorf("Len() = %d, want 1 - re-adding the same PV must not double count", vms.Len())
	}
}

// Add and Remove ran under mutexes taken in opposite orders in the version this was ported from, so
// concurrent adds and removes could deadlock. They also raced on the index maps.
func TestVolumeMetricsConcurrentAddRemove(t *testing.T) {
	vms := NewVolumeMetrics()
	filesystems := make([]uuid.UUID, 4)
	for i := range filesystems {
		filesystems[i] = uuid.New()
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				fs := filesystems[j%len(filesystems)]
				// Deliberately let PVs from different goroutines land on the same inode, which
				// is what static provisioning does.
				inode := uint64(j%10 + 1)
				pv := types.UID(fmt.Sprintf("pv-%d-%d", i, j%10))
				vms.Add(pv, volumeKey{filesystemUid: fs, inodeId: inode}, &VolumeMetric{})
				_ = vms.ForFilesystem(fs)
				_ = vms.ForInode(fs, inode)
				_ = vms.All()
				_ = vms.PvUIDs()
				if j%3 == 0 {
					vms.Remove(pv)
				}
			}
		}(i)
	}
	wg.Wait()

	// Every remaining volume must be reachable both ways, i.e. the two indexes agree.
	for _, uid := range vms.PvUIDs() {
		vm := vms.Get(uid)
		if vm == nil {
			t.Fatalf("%s is listed but not gettable", uid)
		}
		if vm.key.filesystemUid == uuid.Nil {
			continue
		}
		found := false
		for _, other := range vms.ForInode(vm.key.filesystemUid, vm.key.inodeId) {
			if other == vm {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s is in the PV index but not the filesystem index", uid)
		}
	}
}

// Several PersistentVolumes can point at the same path, and so share a filesystem and inode. Each
// must be reachable from a quota entry, and removing one must not unindex the others.
func TestVolumeMetricsSharedInode(t *testing.T) {
	vms := NewVolumeMetrics()
	fs := uuid.New()
	key := volumeKey{filesystemUid: fs, inodeId: 7}
	first, second, third := &VolumeMetric{}, &VolumeMetric{}, &VolumeMetric{}

	vms.Add("pv-a", key, first)
	vms.Add("pv-b", key, second)
	vms.Add("pv-c", key, third)

	if got := vms.ForInode(fs, 7); len(got) != 3 {
		t.Fatalf("ForInode returned %d volumes, want 3 - a quota entry must update every PV "+
			"pointing at that path", len(got))
	}
	if got := vms.ForFilesystem(fs); len(got) != 3 {
		t.Errorf("ForFilesystem returned %d volumes, want 3", len(got))
	}
	if vms.Len() != 3 {
		t.Errorf("Len() = %d, want 3", vms.Len())
	}

	vms.Remove("pv-b")
	got := vms.ForInode(fs, 7)
	if len(got) != 2 {
		t.Fatalf("after removing one of three, ForInode returned %d volumes, want 2", len(got))
	}
	for _, vm := range got {
		if vm == second {
			t.Error("the removed volume is still indexed")
		}
	}

	vms.Remove("pv-a")
	vms.Remove("pv-c")
	if got := vms.ForInode(fs, 7); len(got) != 0 {
		t.Errorf("ForInode returned %v after every sharer was removed", got)
	}
	if got := vms.ForFilesystem(fs); len(got) != 0 {
		t.Errorf("ForFilesystem returned %v after every sharer was removed", got)
	}
}
