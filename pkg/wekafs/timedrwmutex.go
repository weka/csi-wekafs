package wekafs

import (
	"github.com/rs/zerolog/log"
	"sync"
	"time"
)

const LockTimeout = time.Millisecond * 3000

// TimedRWMutex wraps sync.RWMutex with timeout behavior
type TimedRWMutex struct {
	mu sync.RWMutex
}

func (t *TimedRWMutex) Lock() { t.LockWithTimeout(LockTimeout) }

// LockWithTimeout tries to acquire write lock within the given timeout
func (t *TimedRWMutex) LockWithTimeout(timeout time.Duration) {
	done := make(chan struct{})

	go func() {
		t.mu.Lock()
		close(done)
	}()

	select {
	case <-done:
		// Lock acquired
	case <-time.After(timeout):
		log.Error().Msg("LockedRWMutex: Lock() too long")
	}
}

func (t *TimedRWMutex) RLock() { t.RLockWithTimeout(LockTimeout) }

// RLockWithTimeout tries to acquire read lock within the given timeout
func (t *TimedRWMutex) RLockWithTimeout(timeout time.Duration) {
	done := make(chan struct{})

	go func() {
		t.mu.RLock()
		close(done)
	}()

	select {
	case <-done:
		// Lock acquired
	case <-time.After(timeout):
		log.Error().Msg("TimedRWMutex: RLock() too long")
	}
}

func (t *TimedRWMutex) Unlock() {
	t.mu.Unlock()
}

func (t *TimedRWMutex) RUnlock() {
	t.mu.RUnlock()
}
