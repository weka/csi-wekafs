package wekafs

import (
	"reflect"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Constructing the metrics must not touch any registry, so a second metrics server - or a second
// test - does not panic on duplicate registration.
func TestNewPrometheusMetricsRegistersNothing(t *testing.T) {
	first := NewPrometheusMetrics()
	second := NewPrometheusMetrics()

	for name, m := range map[string]*PrometheusMetrics{"first": first, "second": second} {
		reg := prometheus.NewRegistry()
		for _, c := range m.Collectors() {
			if err := reg.Register(c); err != nil {
				t.Fatalf("%s: collector rejected by a fresh registry: %v", name, err)
			}
		}
	}
}

// Every metric the struct declares must appear in Collectors(), or it is built and never exported.
// That is exactly how four node metrics went missing in the version this was ported from, so the
// check walks the struct by reflection rather than trusting the list to be kept in step by hand.
func TestPrometheusMetricsCollectorsAreComplete(t *testing.T) {
	m := NewPrometheusMetrics()

	declared := 0
	v := reflect.ValueOf(m).Elem()
	for i := 0; i < v.NumField(); i++ { // .volumes and .server
		group := v.Field(i)
		if group.Kind() != reflect.Struct {
			continue
		}
		for j := 0; j < group.NumField(); j++ {
			f := group.Field(j)
			if f.Kind() != reflect.Ptr && f.Kind() != reflect.Interface {
				continue
			}
			if f.IsNil() {
				t.Errorf("%s.%s is declared but never constructed",
					v.Type().Field(i).Name, group.Type().Field(j).Name)
				continue
			}
			declared++
		}
	}

	if got := len(m.Collectors()); got != declared {
		t.Errorf("Collectors() returns %d collectors but %d metrics are declared - %d would be "+
			"built and never exported", got, declared, declared-got)
	}
}

// Names must be unique and correctly namespaced, and help must be present and distinct - a metric
// helped with another metric's text is how the source of this port described its failure counters
// as successes.
func TestPrometheusMetricsNamesAndHelp(t *testing.T) {
	m := NewPrometheusMetrics()
	ch := make(chan *prometheus.Desc, 512)
	go func() {
		for _, c := range m.Collectors() {
			c.Describe(ch)
		}
		close(ch)
	}()

	seenName := map[string]bool{}
	seenHelp := map[string]string{}
	for d := range ch {
		desc := d.String()
		name := between(desc, `fqName: "`, `"`)
		help := between(desc, `help: "`, `"`)

		if !strings.HasPrefix(name, MetricsPrefix+"_") {
			t.Errorf("%s is not namespaced with %q", name, MetricsPrefix)
		}
		if seenName[name] {
			t.Errorf("duplicate metric name %s", name)
		}
		seenName[name] = true

		if help == "" {
			t.Errorf("%s has no help text", name)
			continue
		}
		if other, dup := seenHelp[help]; dup {
			t.Errorf("%s and %s share help text %q - one of them is probably describing the other",
				name, other, help)
		}
		seenHelp[help] = name
	}
	if len(seenName) == 0 {
		t.Fatal("no descriptors were produced")
	}
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	s = s[i+len(start):]
	j := strings.Index(s, end)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// TestCsiVolumeLabelValuesMatchLabels is the invariant that fails silently everywhere else: a
// Prometheus vector panics at runtime, not at compile time, when the number of values does not match
// the number of labels. Since these two are edited in different places - a label added to the list,
// a value appended in the builder - they drift easily, and the cost lands on whoever is provisioning
// volumes rather than on whoever made the change.
func TestCsiVolumeLabelValuesMatchLabels(t *testing.T) {
	for _, tc := range []struct {
		name string
		pv   *v1.PersistentVolume
	}{
		{"bound volume", pvWithClaim()},
		{"unbound volume, no claim ref", pvWithoutClaim()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := csiVolumeLabelValues("csi.weka.io", tc.pv, "guid", "fs", "dir/v1", "Root")
			assert.Len(t, values, len(LabelsForCsiVolumes),
				"label values must line up with LabelsForCsiVolumes, or every metric using them panics")
		})
	}
}

// TestCsiVolumeLabelValuesOrder pins the values to their label names. Order is the whole contract
// here: a swap between two string labels produces a metric that is wrong rather than one that fails.
func TestCsiVolumeLabelValuesOrder(t *testing.T) {
	values := csiVolumeLabelValues("csi.weka.io", pvWithClaim(), "cluster-guid", "myfs", "dir/v1", "Tenant1")
	require.Len(t, values, len(LabelsForCsiVolumes))

	got := map[string]string{}
	for i, name := range LabelsForCsiVolumes {
		got[name] = values[i]
	}

	assert.Equal(t, "csi.weka.io", got["csi_driver_name"])
	assert.Equal(t, "pv-1", got["pv_name"])
	assert.Equal(t, "cluster-guid", got["cluster_guid"])
	assert.Equal(t, "sc-weka", got["storage_class_name"])
	assert.Equal(t, "myfs", got["filesystem_name"])
	assert.Equal(t, "dir/v1", got["volume_type"])
	assert.Equal(t, "Tenant1", got["organization"])
	assert.Equal(t, "claim-1", got["pvc_name"])
	assert.Equal(t, "team-a", got["pvc_namespace"])
	assert.Equal(t, "uid-1", got["pvc_uid"])
	assert.Equal(t, "weka-secrets/api-creds", got["secret_name"])
}

// TestCsiVolumeLabelValuesUnbound covers a volume that was provisioned but never bound. It has no
// claim, and the claim labels must be blank rather than absent - a shorter slice would panic.
func TestCsiVolumeLabelValuesUnbound(t *testing.T) {
	values := csiVolumeLabelValues("csi.weka.io", pvWithoutClaim(), "guid", "fs", "dir/v1", "Root")
	require.Len(t, values, len(LabelsForCsiVolumes))

	got := map[string]string{}
	for i, name := range LabelsForCsiVolumes {
		got[name] = values[i]
	}
	assert.Empty(t, got["pvc_name"])
	assert.Empty(t, got["pvc_namespace"])
	assert.Empty(t, got["pvc_uid"])
	assert.Equal(t, "pv-2", got["pv_name"], "the volume itself is still identified")
}

func TestSecretRefLabel(t *testing.T) {
	assert.Equal(t, "weka-secrets/api-creds", secretRefLabel(pvWithClaim()))
	assert.Empty(t, secretRefLabel(pvWithoutClaim()),
		"a volume with no Secret ref must label blank, not panic")
	assert.Empty(t, secretRefLabel(&v1.PersistentVolume{}),
		"a volume with no CSI spec at all must label blank")
}

// TestLabelsForCsiVolumesAreUnique guards against a duplicate label name, which Prometheus rejects
// at registration - turning a typo into a process that fails to start.
func TestLabelsForCsiVolumesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range LabelsForCsiVolumes {
		assert.False(t, seen[l], "duplicate label %q", l)
		seen[l] = true
	}
}

func pvWithClaim() *v1.PersistentVolume {
	pv := pvWithoutClaim()
	pv.Name = "pv-1"
	pv.Spec.ClaimRef = &v1.ObjectReference{Name: "claim-1", Namespace: "team-a", UID: types.UID("uid-1")}
	pv.Spec.CSI.NodeStageSecretRef = &v1.SecretReference{Name: "api-creds", Namespace: "weka-secrets"}
	return pv
}

func pvWithoutClaim() *v1.PersistentVolume {
	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-2"},
		Spec: v1.PersistentVolumeSpec{
			StorageClassName: "sc-weka",
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CSI: &v1.CSIPersistentVolumeSource{Driver: "csi.weka.io", VolumeHandle: "weka/v2/fs/dir"},
			},
		},
	}
}

// Every counter ends with _total. Two did not - read_duration_us and write_duration_us, the only
// counters in the set without the suffix. Under OpenMetrics exposition client_golang appends _total
// to a counter that lacks it, so the series name operators bind to would have depended on the
// scrape format. Metric names are a published contract: cheap to fix now, not fixable once a
// release has exported the series.
//
// This walks the struct by field type rather than matching name fragments. An earlier version of
// this test only looked at names containing _invoke_count, which is exactly how the two duration
// counters passed it - they are neither invoke counters nor *_duration_seconds.
func TestCounterNamesEndWithTotal(t *testing.T) {
	m := NewPrometheusMetrics()

	var offenders []string
	seen := 0
	v := reflect.ValueOf(m).Elem()
	for i := 0; i < v.NumField(); i++ { // .volumes and .server
		group := v.Field(i)
		if group.Kind() != reflect.Struct {
			continue
		}
		for j := 0; j < group.NumField(); j++ {
			field := group.Field(j)
			if !field.CanInterface() {
				field = reflect.NewAt(field.Type(), field.Addr().UnsafePointer()).Elem()
			}
			collector, ok := field.Interface().(prometheus.Collector)
			if !ok || field.IsZero() {
				continue
			}
			if !isCounterType(field.Type()) {
				continue
			}
			ch := make(chan *prometheus.Desc, 8)
			go func() { collector.Describe(ch); close(ch) }()
			for d := range ch {
				name := between(d.String(), `fqName: "`, `"`)
				seen++
				if !strings.HasSuffix(name, "_total") {
					offenders = append(offenders, name)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("no counters were described - the convention would pass vacuously")
	}
	if len(offenders) != 0 {
		t.Errorf("expected every counter to end with _total, these do not: %v", offenders)
	}
}

// isCounterType reports whether a metrics-struct field holds a counter, by type rather than by name,
// so the convention above covers a counter added later without anyone remembering to list it.
func isCounterType(t reflect.Type) bool {
	switch t.String() {
	case "*wekafs.TimedCounter", "*wekafs.TimedCounterVec",
		"prometheus.Counter", "*prometheus.CounterVec":
		return true
	}
	return false
}

// Prometheus convention, which the rest of this repo follows: the histogram owns the plain
// *_duration_seconds name, exposing _bucket/_sum/_count from it, and a counter accumulating seconds
// carries _total. These names were the other way round - the counter held *_duration_seconds and
// the histogram was pushed out to *_duration_seconds_histogram.
//
// A metric name is a published contract, so this is worth holding: both halves have to move
// together, since the histogram cannot take the plain name while a counter still owns it.
func TestDurationMetricNamesFollowConvention(t *testing.T) {
	m := NewPrometheusMetrics()
	ch := make(chan *prometheus.Desc, 512)
	go func() {
		for _, c := range m.Collectors() {
			c.Describe(ch)
		}
		close(ch)
	}()

	var suffixed []string
	seen := 0
	for d := range ch {
		name := between(d.String(), `fqName: "`, `"`)
		if !strings.Contains(name, "duration_seconds") {
			continue
		}
		seen++
		// Nothing should be named *_duration_seconds_histogram: that suffix exists only because the
		// plain name was taken.
		if strings.HasSuffix(name, "_duration_seconds_histogram") {
			suffixed = append(suffixed, name)
		}
	}
	if seen == 0 {
		t.Fatal("no duration metrics were described - the convention would pass vacuously")
	}
	if len(suffixed) != 0 {
		t.Errorf("expected no _duration_seconds_histogram names, the histogram should own the plain name: %v", suffixed)
	}
}

// A metric family must use one prefix for all of its series. The single-PV fetch family broke that:
// invoke, success and failure omitted the "operations" token that its own duration pair carried, and
// that every sibling family - fetch_pv_batch_operations_*, fetch_metrics_batch_operations_* - carries
// throughout. The Go field names said FetchSinglePvMetricsOperations*, so the token was intended and
// three names had simply drifted.
//
// Targeted at that drift rather than at family completeness, because families legitimately differ in
// which counters they have - process_pv_operations has no invoke counter, quota_update_batch no
// failure one. What is never legitimate is the same family appearing under two prefixes that differ
// only by a token.
func TestNoMetricFamilyUsesTwoPrefixes(t *testing.T) {
	m := NewPrometheusMetrics()
	ch := make(chan *prometheus.Desc, 512)
	go func() {
		for _, c := range m.Collectors() {
			c.Describe(ch)
		}
		close(ch)
	}()

	suffixes := []string{
		"_duration_seconds_total", "_duration_seconds",
		"_invoke_count_total", "_success_count_total", "_failure_count_total",
	}
	prefixes := map[string]bool{}
	for d := range ch {
		name := between(d.String(), `fqName: "`, `"`)
		for _, suf := range suffixes {
			if p, ok := strings.CutSuffix(name, suf); ok {
				prefixes[p] = true
				break
			}
		}
	}
	if len(prefixes) == 0 {
		t.Fatal("no metric families were found - the property would pass vacuously")
	}

	for p := range prefixes {
		// The exact drift seen: one family exporting some series under P and others under
		// P_operations. Either spelling is fine; using both for one family is not.
		if prefixes[p+"_operations"] {
			t.Errorf("family is split across two prefixes: %q and %q", p, p+"_operations")
		}
	}
}
