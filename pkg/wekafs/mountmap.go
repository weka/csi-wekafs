/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package wekafs

import (
	"sync"
	"sync/atomic"
)

// mountMap tracks how many callers hold each mount, keyed by a mount point and its options.
//
// The lock is per entry, not per mounter. Callers hold it across the mount and unmount syscalls -
// they have to, or two callers arriving together would both see a zero refcount and both mount - and
// a single mounter-wide lock would therefore serialise every mount on the node behind whichever
// filesystem happened to be mounting. Two filesystems now mount concurrently while two callers of
// the same filesystem still take turns.
type mountMap struct {
	counts sync.Map // refIndex -> *atomic.Int32
	locks  sync.Map // refIndex -> *sync.Mutex
}

func newMountMap() *mountMap {
	return &mountMap{}
}

// lockFor returns the mutex guarding one entry, creating it if this is the first caller. Returned
// unlocked: the caller decides when to take it, and holds it across the mount itself.
func (mm *mountMap) lockFor(refIndex string) *sync.Mutex {
	lock, _ := mm.locks.LoadOrStore(refIndex, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// WithEntry runs fn holding the entry's lock, having loaded or created the refcount underneath it.
//
// The counter is fetched inside the lock on purpose. Handing it back before locking would leave a
// window in which the sweep prunes the entry, so that the next caller creates a second counter for
// the same mount: two callers would then be counting references separately, and whichever reached
// zero first would unmount a filesystem the other still had in use.
func (mm *mountMap) WithEntry(refIndex string, fn func(refCount *atomic.Int32) error) error {
	lock := mm.lockFor(refIndex)
	lock.Lock()
	defer lock.Unlock()

	count, _ := mm.counts.LoadOrStore(refIndex, &atomic.Int32{})
	return fn(count.(*atomic.Int32))
}

// Load returns an existing entry, reporting whether it was there. Unlike WithEntry it does not
// create one, so a caller sweeping the map cannot resurrect entries it is about to prune.
func (mm *mountMap) Load(refIndex string) (*atomic.Int32, *sync.Mutex, bool) {
	count, ok := mm.counts.Load(refIndex)
	if !ok {
		return nil, nil, false
	}
	return count.(*atomic.Int32), mm.lockFor(refIndex), true
}

// Prune drops an entry. The caller must hold the entry's lock and have observed a zero refcount, or
// a mount could be forgotten while still held.
func (mm *mountMap) Prune(refIndex string) {
	mm.counts.Delete(refIndex)
	// The lock itself is deliberately left behind. Deleting it would hand a later caller a different
	// mutex than one still inside the critical section here is holding.
}

// Indexes returns a snapshot of the keys, safe to iterate while other callers mutate the map.
func (mm *mountMap) Indexes() []string {
	var indexes []string
	mm.counts.Range(func(key, _ any) bool {
		indexes = append(indexes, key.(string))
		return true
	})
	return indexes
}

func (mm *mountMap) Len() int {
	count := 0
	mm.counts.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
