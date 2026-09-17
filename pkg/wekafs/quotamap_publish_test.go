package wekafs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// batchRefreshQuotaMaps launches GetMetricsFromQuotaMap with `go` and returns without waiting for
// it, while PeriodicQuotaMapUpdater ticks every minute regardless. A slow pass can therefore still
// be running when the next one starts, and both reach the same tracked volumes.
//
// Publishing a reading must not go through the VolumeMetric the indexes hand out: two passes would
// assign .metrics on the same object, and whichever landed last would decide what the streamer
// reported for both. This runs several passes over one volume at once, so that assignment shows up
// under -race, which is how CI runs the suite.
func TestGetMetricsFromQuotaMapPublishesPerPass(t *testing.T) {
	const passes = 8
	const hard, used = uint64(16 << 30), uint64(8192)
	fsUid, inodeId := uuid.New(), uint64(196592046571530)

	ms := &MetricsServer{
		volumeMetrics: NewVolumeMetrics(),
		// Buffered for every pass, so publishing never blocks on a streamer this test does not run.
		volumeMetricsChan: make(chan *VolumeMetric, passes),
	}
	ms.volumeMetrics.Add(types.UID("2f1b0c6e-0000-4000-8000-000000000003"),
		volumeKey{filesystemUid: fsUid, inodeId: inodeId},
		&VolumeMetric{
			persistentVolume: &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pvc-quota-16gi"}},
			volume:           &Volume{FilesystemName: "quotavols"},
		})

	qm := &apiclient.QuotaMap{
		FileSystemUid: fsUid,
		Quotas:        map[uint64]*apiclient.Quota{inodeId: {HardLimitBytes: hard, TotalBytes: used}},
		LastUpdate:    time.Now(),
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range passes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // released together, so the passes actually overlap
			ms.GetMetricsFromQuotaMap(context.Background(), qm)
		}()
	}
	close(start)
	wg.Wait()

	for i := range passes {
		select {
		case got := <-ms.volumeMetricsChan:
			if got.metrics == nil || got.metrics.Usage == nil {
				t.Fatalf("pass %d published without usage statistics", i)
			}
			if got.metrics.Usage.Used != int64(used) {
				t.Errorf("pass %d: Used = %d, want %d", i, got.metrics.Usage.Used, used)
			}
		default:
			t.Fatalf("only %d of %d passes published a reading", i, passes)
		}
	}
}
