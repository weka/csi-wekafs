package wekafs

import (
	"go.uber.org/atomic"
	"sync"
)

type mountMap struct {
	m sync.Map
	l sync.Map
}

func newMountMap() *mountMap {
	return &mountMap{
		m: sync.Map{},
		l: sync.Map{},
	}
}

func (mm *mountMap) LoadOrStore(s string) (*atomic.Int32, *sync.Mutex) {
	lock := mm.getLock(s)
	lock.Lock()
	defer lock.Unlock()
	val, _ := mm.m.LoadOrStore(s, atomic.NewInt32(0))
	return val.(*atomic.Int32), lock
}

func (mm *mountMap) Load(s string) (*atomic.Int32, *sync.Mutex) {
	lock := mm.getLock(s)
	lock.Lock()
	defer lock.Unlock()
	if refCount, ok := mm.m.Load(s); ok {
		return refCount.(*atomic.Int32), lock
	}
	mm.l.Delete(s)
	return nil, nil
}

func (mm *mountMap) getLock(s string) *sync.Mutex {
	lock, _ := mm.l.LoadOrStore(s, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (mm *mountMap) Prune(s string) {
	lock := mm.getLock(s)
	lock.Lock()
	defer lock.Unlock()
	mm.m.Delete(s)
	// TODO: need to return it?
	// mm.l.Delete(s)
}

func (mm *mountMap) getIndexes() []string {
	var indexes []string
	mm.m.Range(func(key, value interface{}) bool {
		indexes = append(indexes, key.(string))
		return true
	})
	return indexes
}

func (mm *mountMap) Len() int {
	count := 0
	mm.m.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}
