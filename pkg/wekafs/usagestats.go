package wekafs

import (
	"time"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// UsageStats is the capacity view of a volume, derived from its quota. All values are in bytes.
type UsageStats struct {
	Capacity  int64
	Used      int64
	Free      int64
	Timestamp time.Time
}

// PvStats bundles what is known about a persistent volume: how much space it uses, and how it is
// performing. Either half may be nil when only one was collected.
type PvStats struct {
	Usage       *UsageStats
	Performance *apiclient.PerfStats
}
