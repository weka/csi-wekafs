package wekafs

import (
	"testing"
	"time"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// quotaToUsageStats is the single point where a Quota becomes the three numbers a volume reports.
// Free is derived from used, so reading the wrong field would report every volume as both empty and
// entirely free rather than failing outright.
func TestQuotaToUsageStats(t *testing.T) {
	const hard, used = uint64(6442450944), uint64(4096)
	ts := time.Unix(1700000000, 0)

	got := quotaToUsageStats(&apiclient.Quota{HardLimitBytes: hard, UsedBytes: used}, ts)
	if got.Capacity != int64(hard) {
		t.Errorf("Capacity = %d, want %d", got.Capacity, hard)
	}
	if got.Used != int64(used) {
		t.Errorf("Used = %d, want %d", got.Used, used)
	}
	if want := int64(hard - used); got.Free != want {
		t.Errorf("Free = %d, want %d", got.Free, want)
	}
	if !got.Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, ts)
	}
}

// Free is derived, so an empty volume must report the whole quota as free rather than as used.
func TestQuotaToUsageStatsEmptyVolume(t *testing.T) {
	got := quotaToUsageStats(&apiclient.Quota{HardLimitBytes: 1024}, time.Unix(0, 0))
	if got.Used != 0 || got.Free != 1024 || got.Capacity != 1024 {
		t.Errorf("got Capacity=%d Used=%d Free=%d, want 1024/0/1024", got.Capacity, got.Used, got.Free)
	}
}
