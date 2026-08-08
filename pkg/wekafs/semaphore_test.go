package wekafs

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"
)

// An op absent from maxConcurrencyPerOp previously got semaphore.NewWeighted(0), which no acquire
// can ever satisfy: the request blocks until its deadline instead of running. Any RPC added without
// also adding a concurrency entry would hang.
func TestInitializeSemaphoreDefaultsUnknownOpToOne(t *testing.T) {
	for _, tc := range []struct {
		name    string
		acquire func(*testing.T, string) error
	}{
		{"controller", func(t *testing.T, op string) error {
			cs := &ControllerServer{
				semaphores: make(map[string]*semaphore.Weighted),
				config:     &DriverConfig{maxConcurrencyPerOp: map[string]int64{"CreateVolume": 5}},
			}
			cs.initializeSemaphore(context.Background(), op)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			return cs.semaphores[op].Acquire(ctx, 1)
		}},
		{"node", func(t *testing.T, op string) error {
			ns := &NodeServer{
				semaphores: make(map[string]*semaphore.Weighted),
				config:     &DriverConfig{maxConcurrencyPerOp: map[string]int64{"NodePublishVolume": 5}},
			}
			ns.initializeSemaphore(context.Background(), op)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			return ns.semaphores[op].Acquire(ctx, 1)
		}},
	} {
		t.Run(tc.name+"/unknown op is acquirable", func(t *testing.T) {
			if err := tc.acquire(t, "SomeOperationWithNoConfiguredLimit"); err != nil {
				t.Fatalf("acquiring a semaphore for an unconfigured op should succeed, got %v", err)
			}
		})
		t.Run(tc.name+"/configured op keeps its limit", func(t *testing.T) {
			op := map[string]string{"controller": "CreateVolume", "node": "NodePublishVolume"}[tc.name]
			if err := tc.acquire(t, op); err != nil {
				t.Fatalf("acquiring a configured semaphore should succeed, got %v", err)
			}
		})
	}
}

// A configured limit of zero is explicit operator intent to block the op, and must be preserved -
// only an absent entry defaults to 1.
func TestInitializeSemaphoreKeepsExplicitZero(t *testing.T) {
	cs := &ControllerServer{
		semaphores: make(map[string]*semaphore.Weighted),
		config:     &DriverConfig{maxConcurrencyPerOp: map[string]int64{"BlockedOp": 0}},
	}
	cs.initializeSemaphore(context.Background(), "BlockedOp")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := cs.semaphores["BlockedOp"].Acquire(ctx, 1); err == nil {
		t.Fatal("an explicitly configured limit of 0 must still block")
	}
}
