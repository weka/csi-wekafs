package wekafs

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// collect gathers everything a collector emits into dto form, which is what a scrape sees.
func collect(t *testing.T, c prometheus.Collector) []*dto.Metric {
	t.Helper()
	ch := make(chan prometheus.Metric, 128)
	go func() {
		c.Collect(ch)
		close(ch)
	}()
	var out []*dto.Metric
	for m := range ch {
		d := &dto.Metric{}
		if err := m.Write(d); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		out = append(out, d)
	}
	return out
}

func onlyMetric(t *testing.T, c prometheus.Collector) *dto.Metric {
	t.Helper()
	got := collect(t, c)
	if len(got) != 1 {
		t.Fatalf("expected exactly one metric, got %d", len(got))
	}
	return got[0]
}

// The point of the type: the measurement's own time is exported, not the scrape time.
func TestTimedGaugeExportsMeasurementTimestamp(t *testing.T) {
	measured := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	g := NewTimedGauge(prometheus.GaugeOpts{Namespace: "weka", Name: "test_gauge", Help: "h"})
	g.SetWithTimestamp(42, measured)

	m := onlyMetric(t, g)
	if got := m.GetTimestampMs(); got != measured.UnixMilli() {
		t.Errorf("TimestampMs = %d, want %d (the time the value was measured)", got, measured.UnixMilli())
	}
	if got := m.GetGauge().GetValue(); got != 42 {
		t.Errorf("value = %v, want 42", got)
	}
}

// A gauge must be exported as a gauge whether or not a timestamp was supplied. The two paths
// through Collect used to disagree, exporting the timestamped one as a counter.
func TestTimedGaugeIsAGaugeOnBothPaths(t *testing.T) {
	withTs := NewTimedGauge(prometheus.GaugeOpts{Namespace: "weka", Name: "g1", Help: "h"})
	withTs.SetWithTimestamp(7, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	withoutTs := NewTimedGauge(prometheus.GaugeOpts{Namespace: "weka", Name: "g2", Help: "h"})
	withoutTs.SetWithTimestamp(7, time.Time{})

	for name, g := range map[string]*TimedGauge{"timestamped": withTs, "untimestamped": withoutTs} {
		m := onlyMetric(t, g)
		if m.GetGauge() == nil {
			t.Errorf("%s: metric is not a gauge (counter=%v)", name, m.GetCounter())
		}
		if m.GetCounter() != nil {
			t.Errorf("%s: metric was exported as a counter", name)
		}
	}
}

// Set with a zero timestamp means "now", so the sample still carries a time.
func TestTimedGaugeSetUsesNow(t *testing.T) {
	g := NewTimedGauge(prometheus.GaugeOpts{Namespace: "weka", Name: "g", Help: "h"})
	before := time.Now()
	g.Set(1)
	m := onlyMetric(t, g)
	if m.GetTimestampMs() < before.UnixMilli() {
		t.Errorf("TimestampMs = %d, want >= %d", m.GetTimestampMs(), before.UnixMilli())
	}
}

func TestTimedCounterAddAndSet(t *testing.T) {
	c := NewTimedCounter(prometheus.CounterOpts{Namespace: "weka", Name: "c", Help: "h"})
	c.Inc()
	c.Add(4)
	if got := onlyMetric(t, c).GetCounter().GetValue(); got != 5 {
		t.Errorf("after Inc+Add(4), value = %v, want 5", got)
	}

	// Set mirrors an externally accumulated total rather than counting up from here.
	measured := time.Date(2026, 2, 2, 2, 2, 2, 0, time.UTC)
	c.SetWithTimestamp(100, measured)
	m := onlyMetric(t, c)
	if got := m.GetCounter().GetValue(); got != 100 {
		t.Errorf("after Set(100), value = %v, want 100", got)
	}
	if got := m.GetTimestampMs(); got != measured.UnixMilli() {
		t.Errorf("TimestampMs = %d, want %d", got, measured.UnixMilli())
	}
}

func TestTimedHistogramBucketsAreCumulative(t *testing.T) {
	h := NewTimedHistogram(prometheus.HistogramOpts{
		Namespace: "weka", Name: "h", Help: "h", Buckets: []float64{1, 5, 10},
	})
	for _, v := range []float64{0.5, 2, 7, 20} {
		h.Observe(v)
	}

	m := onlyMetric(t, h)
	if got := m.GetHistogram().GetSampleCount(); got != 4 {
		t.Errorf("count = %d, want 4", got)
	}
	if got := m.GetHistogram().GetSampleSum(); got != 29.5 {
		t.Errorf("sum = %v, want 29.5", got)
	}
	want := map[float64]uint64{1: 1, 5: 2, 10: 3} // cumulative: 20 falls outside every bucket
	for _, b := range m.GetHistogram().GetBucket() {
		if got := b.GetCumulativeCount(); got != want[b.GetUpperBound()] {
			t.Errorf("bucket le=%v has %d, want %d", b.GetUpperBound(), got, want[b.GetUpperBound()])
		}
	}
}

func TestTimedVecLifecycle(t *testing.T) {
	v := NewTimedGaugeVec(
		prometheus.GaugeOpts{Namespace: "weka", Name: "v", Help: "h"},
		[]string{"pv", "cluster"},
	)
	if got := v.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0", got)
	}

	// The same label values must return the same child, or updates would be split across series.
	first := v.WithLabelValues("pv-1", "c-1")
	if again := v.WithLabelValues("pv-1", "c-1"); again != first {
		t.Error("WithLabelValues returned a different child for the same labels")
	}
	if other := v.WithLabelValues("pv-2", "c-1"); other == first {
		t.Error("WithLabelValues returned the same child for different labels")
	}
	if got := v.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
	if got := len(collect(t, v)); got != 2 {
		t.Errorf("collected %d series, want 2", got)
	}

	v.DeleteLabelValues("pv-1", "c-1")
	if got := v.Len(); got != 1 {
		t.Errorf("after delete, Len() = %d, want 1", got)
	}
	if got := len(collect(t, v)); got != 1 {
		t.Errorf("after delete, collected %d series, want 1", got)
	}
}

// Label values must not be able to alias each other through the key used to index children.
func TestTimedVecLabelKeysDoNotCollide(t *testing.T) {
	v := NewTimedGaugeVec(prometheus.GaugeOpts{Namespace: "weka", Name: "k", Help: "h"},
		[]string{"a", "b"})
	if v.WithLabelValues("x", "yz") == v.WithLabelValues("xy", "z") {
		t.Error(`{"x","yz"} and {"xy","z"} share a child`)
	}
	if got := v.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2", got)
	}
}

// A scrape calls Collect while the driver is still recording. Iterating the children map while
// another goroutine inserts into it is a fatal runtime error, not merely a reported race.
func TestTimedVecConcurrentCollectAndUpdate(t *testing.T) {
	gauges := NewTimedGaugeVec(prometheus.GaugeOpts{Namespace: "weka", Name: "cg", Help: "h"}, []string{"pv"})
	counters := NewTimedCounterVec(prometheus.CounterOpts{Namespace: "weka", Name: "cc", Help: "h"}, []string{"pv"})
	histograms := NewTimedHistogramVec(prometheus.HistogramOpts{
		Namespace: "weka", Name: "ch", Help: "h", Buckets: []float64{1, 10},
	}, []string{"pv"})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Scrapers.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, c := range []prometheus.Collector{gauges, counters, histograms} {
					ch := make(chan prometheus.Metric, 512)
					c.Collect(ch)
					close(ch)
					for range ch {
					}
				}
			}
		}()
	}

	// Writers, creating new label sets and deleting old ones as volumes come and go.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				pv := fmt.Sprintf("pv-%d-%d", i, j%20)
				gauges.WithLabelValues(pv).SetWithTimestamp(float64(j), time.Now())
				counters.WithLabelValues(pv).Add(1)
				histograms.WithLabelValues(pv).Observe(float64(j % 15))
				if j%10 == 0 {
					gauges.DeleteLabelValues(pv)
					counters.DeleteLabelValues(pv)
					histograms.DeleteLabelValues(pv)
				}
			}
		}(i)
	}

	// Wait for writers, then stop the scrapers.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	close(stop)
	<-done
}

func TestNormalizeLabelNames(t *testing.T) {
	for in, want := range map[string]string{
		"weka/fs-name.thing": "weka_fs_name_thing",
		"plain":              "plain",
		"":                   "",
	} {
		if got := NormalizeLabelName(in); got != want {
			t.Errorf("NormalizeLabelName(%q) = %q, want %q", in, got, want)
		}
	}
	got := NormalizeLabelNames([]string{"a/b", "c-d"})
	if len(got) != 2 || got[0] != "a_b" || got[1] != "c_d" {
		t.Errorf("NormalizeLabelNames = %v, want [a_b c_d]", got)
	}
}

// The collectors must satisfy prometheus.Collector, since they are registered as such.
var (
	_ prometheus.Collector = (*TimedGauge)(nil)
	_ prometheus.Collector = (*TimedCounter)(nil)
	_ prometheus.Collector = (*TimedHistogram)(nil)
	_ prometheus.Collector = (*TimedGaugeVec)(nil)
	_ prometheus.Collector = (*TimedCounterVec)(nil)
	_ prometheus.Collector = (*TimedHistogramVec)(nil)
)

// A registry must accept them, which also exercises Describe.
func TestTimedMetricsRegister(t *testing.T) {
	reg := prometheus.NewRegistry()
	for i, c := range []prometheus.Collector{
		NewTimedGauge(prometheus.GaugeOpts{Namespace: "weka", Name: "rg", Help: "h"}),
		NewTimedCounter(prometheus.CounterOpts{Namespace: "weka", Name: "rc", Help: "h"}),
		NewTimedHistogram(prometheus.HistogramOpts{Namespace: "weka", Name: "rh", Help: "h", Buckets: []float64{1}}),
		NewTimedGaugeVec(prometheus.GaugeOpts{Namespace: "weka", Name: "rgv", Help: "h"}, []string{"pv"}),
		NewTimedCounterVec(prometheus.CounterOpts{Namespace: "weka", Name: "rcv", Help: "h"}, []string{"pv"}),
		NewTimedHistogramVec(prometheus.HistogramOpts{Namespace: "weka", Name: "rhv", Help: "h", Buckets: []float64{1}}, []string{"pv"}),
	} {
		if err := reg.Register(c); err != nil {
			t.Errorf("collector %d failed to register: %v", i, err)
		}
	}
	if _, err := reg.Gather(); err != nil {
		t.Errorf("Gather failed: %v", err)
	}
}
