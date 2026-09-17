package wekafs

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// newQuotaMapMissTestServer builds the least a MetricsServer needs for
// reportVolumesMissingFromQuotaMap: the fallback consults the quota cache validity before deciding
// to refetch, counts the miss against the driver name, and hands the volume on through
// volumeMetricsChan. The channel is buffered, so handing a volume on does not block on a reader
// these tests do not run.
//
// observedFilesystems is left empty deliberately: GetApiClient tolerates an absent filesystem, and
// the cluster GUID is then simply unknown. The counter must still be recorded - a miss that goes
// uncounted is the failure mode this whole path exists to avoid.
func newQuotaMapMissTestServer(t *testing.T, driverName string, chanSize int) *MetricsServer {
	t.Helper()
	return &MetricsServer{
		driver:              &WekaFsDriver{name: driverName},
		config:              &DriverConfig{quotaCacheValidityDuration: 5 * time.Minute},
		volumeMetrics:       NewVolumeMetrics(),
		prometheusMetrics:   NewPrometheusMetrics(),
		volumeMetricsChan:   make(chan *VolumeMetric, chanSize),
		observedFilesystems: NewObservedFilesystems(),
	}
}

// reportVolumesMissingFromQuotaMap only runs when a filesystem's quota map has no entry for a
// tracked volume - in practice, only for snapshot-backed volumes, whose quota lives in a snapshot
// view the listing does not return. That makes it a path an entire fleet of directory-backed volumes
// never touches, so a defect in it survives any amount of ordinary running.
//
// It is also a path that increments a CounterVec, and prometheus.WithLabelValues *panics* on a label
// count mismatch rather than returning an error. Passing the wrong number of labels here therefore
// crashes the collector the first time a snapshot-backed volume is seen, which is exactly what
// shipped: the counter is built with LabelsForFilesystemOps (three labels) and the call site passed
// two. This test executes the increment, so the cardinality is checked by running it rather than by
// reading it.
func TestReportVolumesMissingFromQuotaMapRecordsTheMiss(t *testing.T) {
	const driverName, fsName = "csi.weka.io.test", "snapvols"
	fsUid, inodeId := uuid.New(), uint64(196592046571528)
	pvUID := types.UID("2f1b0c6e-0000-4000-8000-000000000001")

	ms := newQuotaMapMissTestServer(t, driverName, 1)

	vm := &VolumeMetric{
		persistentVolume: &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pvc-snap-8gi"}},
		volume: &Volume{
			FilesystemName: fsName,
			// A fresh cached reading, so the fallback is served from cache and the test needs no
			// Weka cluster. The fallback's caching behaviour is what bounds its cost in production.
			lastUsageStats: &UsageStats{Capacity: 8 << 30, Used: 4096, Free: 8<<30 - 4096,
				Timestamp: time.Now()},
		},
	}
	ms.volumeMetrics.Add(pvUID, volumeKey{filesystemUid: fsUid, inodeId: inodeId}, vm)

	qm := &apiclient.QuotaMap{FileSystemUid: fsUid}

	// The assertion is partly that this returns at all: a wrong label count panics here.
	ms.reportVolumesMissingFromQuotaMap(context.Background(), qm, inodeId)

	if got := counterValue(t, ms.prometheusMetrics.server.QuotaMapMissCount,
		driverName, "", fsName); got != 1 {
		t.Errorf("QuotaMapMissCount = %v, want 1", got)
	}

	select {
	case got := <-ms.volumeMetricsChan:
		if got.metrics == nil || got.metrics.Usage == nil {
			t.Fatal("volume was handed on without usage statistics")
		}
		if got.metrics.Usage.Used != 4096 {
			t.Errorf("Used = %d, want 4096", got.metrics.Usage.Used)
		}
	default:
		t.Error("volume absent from the quota map was not handed on for reporting; it would " +
			"silently stop appearing in Prometheus")
	}
}

// The fallback runs in goroutines nobody waits for, so a slow pass can still be running when the
// next minute's tick starts another one over the same volumes. Each pass must publish its own
// reading rather than assigning to the shared VolumeMetric the indexes hand out - the failure this
// guards is the last writer deciding what the streamer reports for everyone.
func TestReportVolumesMissingFromQuotaMapPublishesPerPass(t *testing.T) {
	const driverName, fsName = "csi.weka.io.test", "snapvols"
	const passes = 8
	fsUid, inodeId := uuid.New(), uint64(196592046571529)
	pvUID := types.UID("2f1b0c6e-0000-4000-8000-000000000002")

	ms := newQuotaMapMissTestServer(t, driverName, passes)
	vm := &VolumeMetric{
		persistentVolume: &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pvc-snap-16gi"}},
		volume: &Volume{
			FilesystemName: fsName,
			// Fresh, so every pass is served from cache and none of them needs a cluster.
			lastUsageStats: &UsageStats{Capacity: 16 << 30, Used: 8192, Free: 16<<30 - 8192,
				Timestamp: time.Now()},
		},
	}
	ms.volumeMetrics.Add(pvUID, volumeKey{filesystemUid: fsUid, inodeId: inodeId}, vm)

	qm := &apiclient.QuotaMap{FileSystemUid: fsUid}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range passes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // released together, so the passes actually overlap
			ms.reportVolumesMissingFromQuotaMap(context.Background(), qm, inodeId)
		}()
	}
	close(start)
	wg.Wait()

	if got := counterValue(t, ms.prometheusMetrics.server.QuotaMapMissCount,
		driverName, "", fsName); got != passes {
		t.Errorf("QuotaMapMissCount = %v, want %d", got, passes)
	}
	for i := range passes {
		select {
		case got := <-ms.volumeMetricsChan:
			if got.metrics == nil || got.metrics.Usage == nil {
				t.Fatalf("pass %d handed the volume on without usage statistics", i)
			}
			if got.metrics.Usage.Used != 8192 {
				t.Errorf("pass %d: Used = %d, want 8192", i, got.metrics.Usage.Used)
			}
		default:
			t.Fatalf("only %d of %d passes handed the volume on", i, passes)
		}
	}
}

// The cache *write* only happens when a fetch succeeds, and the real fetch resolves an inode -
// falling back to mounting the filesystem - so it is unreachable from a unit test without the
// fetchUsageStats seam. With it, this is the scenario the lock exists for: overlapping passes over
// one volume whose cached reading has expired. Exactly one fetch must happen and the rest must find
// the cache warm, which is only true if the lock spans the fetch rather than just the field; and
// the write itself must not race, which -race checks.
func TestFetchPvUsageStatsFromWekaWithCacheFetchesOncePerExpiry(t *testing.T) {
	const passes = 8
	const used = int64(1 << 20)

	ms := newQuotaMapMissTestServer(t, "csi.weka.io.test", passes)
	var fetches atomic.Int64
	ms.fetchUsageStats = func(ctx context.Context, vm *VolumeMetric) (*UsageStats, error) {
		fetches.Add(1)
		// Slow enough that the other passes are certainly waiting on the lock, not merely
		// scheduled after the first one finished.
		time.Sleep(10 * time.Millisecond)
		return &UsageStats{Capacity: 32 << 30, Used: used, Free: 32<<30 - used, Timestamp: time.Now()}, nil
	}

	vm := &VolumeMetric{
		persistentVolume: &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pvc-snap-32gi"}},
		volume: &Volume{
			FilesystemName: "snapvols",
			// Older than quotaCacheValidityDuration, so the first pass through has to fetch.
			lastUsageStats: &UsageStats{Timestamp: time.Now().Add(-time.Hour)},
		},
	}

	got := make([]*UsageStats, passes)
	errs := make([]error, passes)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range passes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got[i], errs[i] = ms.fetchPvUsageStatsFromWekaWithCache(context.Background(), vm)
		}(i)
	}
	close(start)
	wg.Wait()

	if n := fetches.Load(); n != 1 {
		t.Errorf("fetched %d times, want 1 - overlapping passes must collapse onto one API call", n)
	}
	for i := range passes {
		if errs[i] != nil {
			t.Fatalf("pass %d: %v", i, errs[i])
		}
		if got[i] == nil || got[i].Used != used {
			t.Errorf("pass %d got %v, want the fetched reading", i, got[i])
		}
	}
}

// An inode nothing is tracked at must not be counted as a miss - that would make the counter grow
// with unrelated quotas rather than with volumes the map could not serve.
func TestReportVolumesMissingFromQuotaMapIgnoresUntrackedInodes(t *testing.T) {
	ms := newQuotaMapMissTestServer(t, "csi.weka.io.test", 1)
	ms.reportVolumesMissingFromQuotaMap(context.Background(),
		&apiclient.QuotaMap{FileSystemUid: uuid.New()}, 12345)

	if n := testutilCollectAndCount(ms.prometheusMetrics.server.QuotaMapMissCount); n != 0 {
		t.Errorf("QuotaMapMissCount has %d series, want 0", n)
	}
}

func counterValue(t *testing.T, vec *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	c, err := vec.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%v): %v", labels, err)
	}
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return m.GetCounter().GetValue()
}

func testutilCollectAndCount(c prometheus.Collector) int {
	ch := make(chan prometheus.Metric, 64)
	go func() { c.Collect(ch); close(ch) }()
	n := 0
	for range ch {
		n++
	}
	return n
}
