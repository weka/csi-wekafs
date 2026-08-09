package wekafs

import (
	"reflect"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
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
		declared += declaredMetricFields(t, group, v.Type().Field(i).Name)
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
