package wekafs

import (
	"context"
	"golang.org/x/sync/semaphore"
	"sync"
)

// SemaphoreWrapper wraps semaphore.Weighted and tracks acquired permits.
type SemaphoreWrapper struct {
	sem             *semaphore.Weighted
	acquiredPermits int64
	mu              sync.Mutex
}

// NewSemaphoreWrapper creates a new SemaphoreWrapper.
func NewSemaphoreWrapper(weight int64) *SemaphoreWrapper {
	return &SemaphoreWrapper{
		sem: semaphore.NewWeighted(weight),
	}
}

// Acquire acquires the specified number of permits.
func (sw *SemaphoreWrapper) Acquire(ctx context.Context, n int64) error {
	err := sw.sem.Acquire(ctx, n)
	if err == nil {
		sw.mu.Lock()
		sw.acquiredPermits += n
		sw.mu.Unlock()
	}
	return err
}

// Release releases the specified number of permits.
func (sw *SemaphoreWrapper) Release(n int64) {
	sw.sem.Release(n)
	sw.mu.Lock()
	sw.acquiredPermits -= n
	sw.mu.Unlock()
}

// CurrentCount returns the current number of acquired permits.
func (sw *SemaphoreWrapper) CurrentCount() int64 {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.acquiredPermits
}
