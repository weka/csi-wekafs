package wekafs

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// atomicFloat64 is a float64 usable without a lock, stored as its bit pattern. The standard library
// has no atomic float, and these values are written by collection goroutines while a scrape reads
// them.
type atomicFloat64 struct{ bits atomic.Uint64 }

func (f *atomicFloat64) Load() float64   { return math.Float64frombits(f.bits.Load()) }
func (f *atomicFloat64) Store(v float64) { f.bits.Store(math.Float64bits(v)) }

func (f *atomicFloat64) Add(delta float64) {
	for {
		old := f.bits.Load()
		next := math.Float64bits(math.Float64frombits(old) + delta)
		if f.bits.CompareAndSwap(old, next) {
			return
		}
	}
}

// atomicTime holds a measurement time as Unix nanoseconds, where zero means "no timestamp was
// recorded" and the sample is left for Prometheus to stamp at scrape time.
type atomicTime struct{ nanos atomic.Int64 }

func (t *atomicTime) Store(ts time.Time) {
	if ts.IsZero() {
		t.nanos.Store(0)
		return
	}
	t.nanos.Store(ts.UnixNano())
}

func (t *atomicTime) Load() time.Time {
	nanos := t.nanos.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// Timed metrics carry the time their value was measured, rather than being stamped with the moment
// Prometheus happens to scrape them.
//
// The driver reads much of what it exports from the Weka API on its own schedule, and serves it
// from a cache in between. Exporting a quota fetched three minutes ago as though it were sampled at
// scrape time would misdate it; every rate and every graph would attribute the change to the wrong
// instant. These collectors attach the measurement's own timestamp instead, via
// prometheus.NewMetricWithTimestamp.
//
// Writing a value without supplying a time records it as measured now, so the sample still carries
// one. Only a metric that has never been written is exported untimestamped, leaving Prometheus to
// stamp it at scrape time as usual.
//
// Note this requires the scrape config to honor timestamps, which is the default.

// TimedGauge is a gauge that remembers when its value was measured.
type TimedGauge struct {
	desc   *prometheus.Desc
	val    atomicFloat64
	lastTs atomicTime
	labels []string
}

func newTimedGauge(desc *prometheus.Desc, labels []string) *TimedGauge {
	return &TimedGauge{desc: desc, labels: labels}
}

func NewTimedGauge(opts prometheus.GaugeOpts) *TimedGauge {
	return newTimedGauge(timedDesc(opts.Namespace, opts.Subsystem, opts.Name, opts.Help, nil), nil)
}

// Set records a value measured now.
func (tg *TimedGauge) Set(v float64) {
	tg.SetWithTimestamp(v, time.Time{})
}

// SetWithTimestamp records a value along with the time it was measured. A zero ts means "now".
func (tg *TimedGauge) SetWithTimestamp(v float64, ts time.Time) *prometheus.Desc {
	tg.val.Store(v)
	tg.lastTs.Store(orNow(ts))
	return tg.desc
}

func (tg *TimedGauge) Describe(ch chan<- *prometheus.Desc) { ch <- tg.desc }

func (tg *TimedGauge) Collect(ch chan<- prometheus.Metric) {
	ch <- withTimestamp(
		prometheus.MustNewConstMetric(tg.desc, prometheus.GaugeValue, tg.val.Load(), tg.labels...),
		tg.lastTs.Load(),
	)
}

// TimedCounter is a counter that remembers when its value was measured.
type TimedCounter struct {
	desc   *prometheus.Desc
	val    atomicFloat64
	lastTs atomicTime
	labels []string
}

func newTimedCounter(desc *prometheus.Desc, labels []string) *TimedCounter {
	return &TimedCounter{desc: desc, labels: labels}
}

func NewTimedCounter(opts prometheus.CounterOpts) *TimedCounter {
	return newTimedCounter(timedDesc(opts.Namespace, opts.Subsystem, opts.Name, opts.Help, nil), nil)
}

func (tc *TimedCounter) Inc() { tc.Add(1) }

func (tc *TimedCounter) Add(v float64) {
	tc.val.Add(v)
	tc.lastTs.Store(time.Now())
}

// AddWithTimestamp increases the counter and records when the increase was observed.
func (tc *TimedCounter) AddWithTimestamp(v float64, ts time.Time) {
	tc.val.Add(v)
	tc.lastTs.Store(orNow(ts))
}

// Set overwrites the counter instead of increasing it.
//
// A Prometheus counter is normally monotonic and only ever added to. This exists for values that
// are already accumulated on the other side - the Weka cluster's own counters - where the driver is
// mirroring an external total rather than counting events itself. Mirroring keeps the series
// correct across a driver restart, which re-counting from zero would not. Do not use it for
// anything the driver counts itself.
func (tc *TimedCounter) Set(v float64) {
	tc.SetWithTimestamp(v, time.Time{})
}

// SetWithTimestamp overwrites the counter and records when the value was measured. See Set.
func (tc *TimedCounter) SetWithTimestamp(v float64, ts time.Time) *prometheus.Desc {
	tc.val.Store(v)
	tc.lastTs.Store(orNow(ts))
	return tc.desc
}

func (tc *TimedCounter) Describe(ch chan<- *prometheus.Desc) { ch <- tc.desc }

func (tc *TimedCounter) Collect(ch chan<- prometheus.Metric) {
	ch <- withTimestamp(
		prometheus.MustNewConstMetric(tc.desc, prometheus.CounterValue, tc.val.Load(), tc.labels...),
		tc.lastTs.Load(),
	)
}

// TimedHistogram is a histogram that remembers when it was last observed into.
type TimedHistogram struct {
	desc       *prometheus.Desc
	buckets    map[float64]*atomic.Uint64
	bucketDefs []float64
	sum        atomicFloat64
	count      atomic.Uint64
	lastTs     atomicTime
	labels     []string
}

func newTimedHistogram(desc *prometheus.Desc, bucketDefs []float64, labels []string) *TimedHistogram {
	buckets := make(map[float64]*atomic.Uint64, len(bucketDefs))
	for _, b := range bucketDefs {
		buckets[b] = new(atomic.Uint64)
	}
	return &TimedHistogram{
		desc:       desc,
		buckets:    buckets,
		bucketDefs: bucketDefs,
		labels:     labels,
	}
}

func NewTimedHistogram(opts prometheus.HistogramOpts) *TimedHistogram {
	return newTimedHistogram(
		timedDesc(opts.Namespace, opts.Subsystem, opts.Name, opts.Help, nil), opts.Buckets, nil)
}

func (th *TimedHistogram) Observe(v float64) {
	th.ObserveWithTimestamp(v, time.Time{})
}

// ObserveWithTimestamp records an observation along with the time it was taken.
func (th *TimedHistogram) ObserveWithTimestamp(v float64, ts time.Time) {
	th.count.Add(1)
	th.sum.Add(v)
	// Prometheus histogram buckets are cumulative: an observation counts in its own bucket and in
	// every wider one.
	for _, b := range th.bucketDefs {
		if v <= b {
			th.buckets[b].Add(1)
		}
	}
	th.lastTs.Store(orNow(ts))
}

func (th *TimedHistogram) Describe(ch chan<- *prometheus.Desc) { ch <- th.desc }

func (th *TimedHistogram) Collect(ch chan<- prometheus.Metric) {
	buckets := make(map[float64]uint64, len(th.buckets))
	for b, c := range th.buckets {
		buckets[b] = c.Load()
	}
	ch <- withTimestamp(
		prometheus.MustNewConstHistogram(th.desc, th.count.Load(), th.sum.Load(), buckets, th.labels...),
		th.lastTs.Load(),
	)
}

// timedMetric is what a vec holds: something that describes and collects itself.
type timedMetric interface {
	Describe(chan<- *prometheus.Desc)
	Collect(chan<- prometheus.Metric)
}

// timedVec is the shared machinery behind the vec types: a set of child metrics keyed by label
// values, created on first use.
//
// It exists so the locking is written once. Each vec type used to carry its own copy, and they
// disagreed - one collected under a read lock, another collected with no lock at all, and a third
// deleted with no lock. Iterating the map while another goroutine inserts into it is a fatal
// runtime error, not a race the detector merely reports, and a Prometheus scrape calls Collect
// concurrently with whatever is updating the metrics.
type timedVec[T timedMetric] struct {
	mu     sync.RWMutex
	desc   *prometheus.Desc
	values map[string]T
	newVal func(labels []string) T
}

func newTimedVec[T timedMetric](desc *prometheus.Desc, newVal func(labels []string) T) timedVec[T] {
	return timedVec[T]{
		desc:   desc,
		values: make(map[string]T),
		newVal: newVal,
	}
}

// key joins label values with a separator that cannot appear in one, so distinct label sets cannot
// produce the same key. A hash would be smaller but could collide, silently merging two series.
func labelsKey(values []string) string {
	return strings.Join(values, "\x00")
}

func (v *timedVec[T]) withLabelValues(lv ...string) T {
	key := labelsKey(lv)

	v.mu.RLock()
	existing, ok := v.values[key]
	v.mu.RUnlock()
	if ok {
		return existing
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	// Re-check: another goroutine may have created it while the lock was not held. Returning the
	// one already in the map keeps every caller updating the same child metric.
	if existing, ok := v.values[key]; ok {
		return existing
	}
	created := v.newVal(lv)
	v.values[key] = created
	return created
}

// DeleteLabelValues stops exporting the series for a label set, e.g. once its volume is gone.
func (v *timedVec[T]) DeleteLabelValues(labelValues ...string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.values, labelsKey(labelValues))
}

// Len reports how many series the vec currently exports.
func (v *timedVec[T]) Len() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.values)
}

func (v *timedVec[T]) Describe(ch chan<- *prometheus.Desc) { ch <- v.desc }

func (v *timedVec[T]) Collect(ch chan<- prometheus.Metric) {
	// Collect the children into a slice under the lock, then send them without it: sending on the
	// channel blocks until the registry reads, and holding the lock across that would stall every
	// goroutine trying to record a metric for as long as the scrape takes.
	v.mu.RLock()
	children := make([]T, 0, len(v.values))
	for _, child := range v.values {
		children = append(children, child)
	}
	v.mu.RUnlock()

	for _, child := range children {
		child.Collect(ch)
	}
}

// TimedGaugeVec is a set of TimedGauges, one per label combination.
type TimedGaugeVec struct{ timedVec[*TimedGauge] }

func NewTimedGaugeVec(opts prometheus.GaugeOpts, labels []string) *TimedGaugeVec {
	desc := timedDesc(opts.Namespace, opts.Subsystem, opts.Name, opts.Help, labels)
	return &TimedGaugeVec{newTimedVec(desc, func(lv []string) *TimedGauge {
		return newTimedGauge(desc, lv)
	})}
}

func (v *TimedGaugeVec) WithLabelValues(lv ...string) *TimedGauge { return v.withLabelValues(lv...) }

// TimedCounterVec is a set of TimedCounters, one per label combination.
type TimedCounterVec struct{ timedVec[*TimedCounter] }

func NewTimedCounterVec(opts prometheus.CounterOpts, labels []string) *TimedCounterVec {
	desc := timedDesc(opts.Namespace, opts.Subsystem, opts.Name, opts.Help, labels)
	return &TimedCounterVec{newTimedVec(desc, func(lv []string) *TimedCounter {
		return newTimedCounter(desc, lv)
	})}
}

func (v *TimedCounterVec) WithLabelValues(lv ...string) *TimedCounter {
	return v.withLabelValues(lv...)
}

// TimedHistogramVec is a set of TimedHistograms, one per label combination.
type TimedHistogramVec struct{ timedVec[*TimedHistogram] }

func NewTimedHistogramVec(opts prometheus.HistogramOpts, labels []string) *TimedHistogramVec {
	desc := timedDesc(opts.Namespace, opts.Subsystem, opts.Name, opts.Help, labels)
	buckets := opts.Buckets
	return &TimedHistogramVec{newTimedVec(desc, func(lv []string) *TimedHistogram {
		return newTimedHistogram(desc, buckets, lv)
	})}
}

func (v *TimedHistogramVec) WithLabelValues(lv ...string) *TimedHistogram {
	return v.withLabelValues(lv...)
}

// timedDesc builds the descriptor, marking the help text so a reader of the exposed metrics knows
// the sample carries its own timestamp.
func timedDesc(namespace, subsystem, name, help string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc(
		prometheus.BuildFQName(namespace, subsystem, name),
		help+" (timestamped)",
		labels,
		nil,
	)
}

// orNow substitutes the current time for a zero timestamp, so callers that have no measurement time
// can use the same setters.
func orNow(ts time.Time) time.Time {
	if ts.IsZero() {
		return time.Now()
	}
	return ts
}

// withTimestamp attaches a measurement time to a metric, unless there is none to attach - in which
// case Prometheus stamps it at scrape time.
func withTimestamp(m prometheus.Metric, ts time.Time) prometheus.Metric {
	if ts.IsZero() {
		return m
	}
	return prometheus.NewMetricWithTimestamp(ts, m)
}

// NormalizeLabelName replaces characters that are not valid in a Prometheus label name.
func NormalizeLabelName(str string) string {
	return strings.NewReplacer("/", "_", "-", "_", ".", "_").Replace(str)
}

func NormalizeLabelNames(labels []string) []string {
	normalized := make([]string, len(labels))
	for i, label := range labels {
		normalized[i] = NormalizeLabelName(label)
	}
	return normalized
}
