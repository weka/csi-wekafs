package wekafs

import (
	"reflect"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// declaredMetricFields counts the constructed metric fields on a metrics struct, failing on any that
// were declared but never built. Takes a reflect.Value so it serves both the pointer-to-struct groups
// here and the embedded struct groups in prometheus_test.go.
func declaredMetricFields(t *testing.T, v reflect.Value, groupName string) int {
	t.Helper()

	declared := 0
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() != reflect.Ptr && f.Kind() != reflect.Interface {
			continue
		}
		if f.IsNil() {
			t.Errorf("%s.%s is declared but never constructed", groupName, v.Type().Field(i).Name)
			continue
		}
		declared++
	}
	return declared
}

// Every metric these structs declare must appear in Collectors(), or it is built and never exported.
// The struct and its Collectors() list are edited by hand in two places, so adding a metric to one
// and forgetting the other is the easy mistake; this walks the struct by reflection instead of
// trusting the two to be kept in step.
func TestControllerAndNodeCollectorsAreComplete(t *testing.T) {
	controller := NewControllerServerMetrics()
	node := NewNodeServerMetrics()

	cases := []struct {
		name       string
		collectors []prometheus.Collector
		groups     map[string]any
	}{
		{
			name:       "controller",
			collectors: controller.Collectors(),
			groups: map[string]any{
				"ControllerOperationMetrics":   controller.Operations,
				"ControllerConcurrencyMetrics": controller.Concurrency,
			},
		},
		{
			name:       "node",
			collectors: node.Collectors(),
			groups: map[string]any{
				"NodeServerOperationMetrics":   node.Operations,
				"NodeServerConcurrencyMetrics": node.Concurrency,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			declared := 0
			for groupName, group := range tc.groups {
				declared += declaredMetricFields(t, reflect.ValueOf(group).Elem(), groupName)
			}
			if got := len(tc.collectors); got != declared {
				t.Errorf("Collectors() returns %d collectors but %d metrics are declared - %d would be "+
					"built and never exported", got, declared, declared-got)
			}
		})
	}
}

// Constructing the metrics must not touch any registry, and every collector must be acceptable to a
// fresh one - a duplicate name or an inconsistent label set only surfaces at registration.
func TestControllerAndNodeCollectorsRegisterCleanly(t *testing.T) {
	for name, collectors := range map[string][]prometheus.Collector{
		"controller": NewControllerServerMetrics().Collectors(),
		"node":       NewNodeServerMetrics().Collectors(),
	} {
		reg := prometheus.NewRegistry()
		for _, c := range collectors {
			if err := reg.Register(c); err != nil {
				t.Errorf("%s: collector rejected by a fresh registry: %v", name, err)
			}
		}
	}
}

// The expansion counter records bytes added, so it must ignore anything that did not add bytes.
// A counter cannot be walked back, so every one of these cases would permanently inflate an
// operator's view of how much capacity the driver handed out.
func TestNetExpansion(t *testing.T) {
	const gib = 1 << 30

	for name, tc := range map[string]struct {
		succeeded          bool
		previous, capacity int64
		want               float64
	}{
		"grew by 1GiB":                  {true, gib, 2 * gib, gib},
		"failed after reading old size": {false, gib, 2 * gib, 0},
		"old size never read":           {true, -1, 2 * gib, 0},
		"no-op expand to same size":     {true, gib, gib, 0},
		"shrink request":                {true, 2 * gib, gib, 0},
		"failed before anything known":  {false, -1, -1, 0},
	} {
		if got := netExpansion(tc.succeeded, tc.previous, tc.capacity); got != tc.want {
			t.Errorf("%s: netExpansion(%v, %d, %d) = %v, want %v",
				name, tc.succeeded, tc.previous, tc.capacity, got, tc.want)
		}
	}
}
