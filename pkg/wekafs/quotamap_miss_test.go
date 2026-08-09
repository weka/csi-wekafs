package wekafs

import (
	"context"
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

	ms := &MetricsServer{
		driver: &WekaFsDriver{name: driverName},
		// The fallback consults the quota cache validity before deciding to refetch.
		config:            &DriverConfig{quotaCacheValidityDuration: 5 * time.Minute},
		volumeMetrics:     NewVolumeMetrics(),
		prometheusMetrics: NewPrometheusMetrics(),
		// Buffered, so handing the volume on does not block on a reader this test does not run.
		volumeMetricsChan: make(chan *VolumeMetric, 1),
		// Left nil deliberately: GetApiClient tolerates an absent filesystem, and the cluster GUID
		// is then simply unknown. The counter must still be recorded - a miss that goes uncounted is
		// the failure mode this whole path exists to avoid.
		observedFilesystems: NewObservedFilesystems(),
	}

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

// An inode nothing is tracked at must not be counted as a miss - that would make the counter grow
// with unrelated quotas rather than with volumes the map could not serve.
func TestReportVolumesMissingFromQuotaMapIgnoresUntrackedInodes(t *testing.T) {
	ms := &MetricsServer{
		driver:              &WekaFsDriver{name: "csi.weka.io.test"},
		config:              &DriverConfig{quotaCacheValidityDuration: 5 * time.Minute},
		volumeMetrics:       NewVolumeMetrics(),
		prometheusMetrics:   NewPrometheusMetrics(),
		volumeMetricsChan:   make(chan *VolumeMetric, 1),
		observedFilesystems: NewObservedFilesystems(),
	}
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
