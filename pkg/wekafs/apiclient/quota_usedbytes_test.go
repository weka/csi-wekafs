package apiclient

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient/apiclienttest"
)

// Weka names a quota's consumed capacity differently depending on which endpoint returned it:
// fetching one quota by inode gives used_bytes and no total_bytes at all, while listing a
// filesystem's quotas gives total_bytes and no used_bytes. Each struct must decode the shape of the
// endpoint it is actually used for; getting this backwards reports every volume as empty rather than
// failing, so only a test that pins the wire keys catches it.
//
// Bodies below are trimmed from real responses of a Weka 4.x cluster.
func TestQuotaWireShapesMatchTheirEndpoints(t *testing.T) {
	const singleQuotaBody = `{"inode_id":196592046571528,"used_bytes":4096,
	                          "hard_limit_bytes":6442450944,"soft_limit_bytes":6442450944,
	                          "snap_view_id":8,"status":"ACTIVE"}`
	const listEntryBody = `{"inode_id":11326619722725392385,"total_bytes":4096,
	                        "hard_limit_bytes":524288000,"soft_limit_bytes":524288000,
	                        "snap_view_id":1,"status":"ACTIVE",
	                        "full_path":"default:/csi-volumes/pvc-1e69e28b"}`

	t.Run("Quota decodes the single-quota endpoint", func(t *testing.T) {
		q := &Quota{}
		if err := json.Unmarshal([]byte(singleQuotaBody), q); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if q.UsedBytes != 4096 {
			t.Errorf("UsedBytes = %d, want 4096 - Quota must decode used_bytes, which is the only "+
				"name this endpoint uses", q.UsedBytes)
		}
		if q.HardLimitBytes != 6442450944 {
			t.Errorf("HardLimitBytes = %d, want 6442450944", q.HardLimitBytes)
		}
	})

	t.Run("QuotaInList decodes the listing", func(t *testing.T) {
		q := &QuotaInList{}
		if err := json.Unmarshal([]byte(listEntryBody), q); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if q.TotalBytes != 4096 {
			t.Errorf("TotalBytes = %d, want 4096 - the listing calls consumed capacity total_bytes",
				q.TotalBytes)
		}
	})

	// The listing's shape must not decode into Quota, and vice versa. If either did, the two structs
	// would be interchangeable and the distinction they exist to draw would be silently lost.
	t.Run("the shapes are not interchangeable", func(t *testing.T) {
		q := &Quota{}
		if err := json.Unmarshal([]byte(listEntryBody), q); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if q.UsedBytes != 0 {
			t.Errorf("UsedBytes = %d, want 0: a listing entry carries no used_bytes, so decoding one "+
				"into Quota must not appear to succeed", q.UsedBytes)
		}
	})
}

// End to end over the listing endpoint that batch mode reads, against a fake serving the real wire
// shape. GetQuotaMap is the single place the listing's total_bytes is translated into Quota's
// UsedBytes, so this is what proves the translation is actually applied.
func TestGetQuotaMapTranslatesListedUsedCapacity(t *testing.T) {
	quietLogs(t)
	server := apiclienttest.New(t)
	ctx := context.Background()

	client, err := NewApiClient(ctx, Credentials{
		Username: "admin", Password: "password", Organization: "Root",
		HttpScheme: "http", Endpoints: []string{server.Addr()},
	}, ApiClientOptions{AllowInsecureHttps: true, Hostname: "test", DriverName: "csi.weka.io.test"})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	if err := client.Login(ctx); err != nil {
		t.Fatalf("failed to log in: %v", err)
	}

	server.AddFilesystem("quotamap-fs", 1<<40, 1<<39)
	fs, err := client.GetFileSystemByName(ctx, "quotamap-fs")
	if err != nil {
		t.Fatalf("GetFileSystemByName: %v", err)
	}

	const inode, hard, used = uint64(4242), uint64(524288000), uint64(4096)
	if err := client.UpdateQuota(ctx, NewQuotaUpdateRequest(*fs, inode, QuotaTypeHard, hard), &Quota{}); err != nil {
		t.Fatalf("seeding a quota: %v", err)
	}
	server.SetQuotaUsedBytes(fs.Uid.String(), inode, used)

	qm, err := client.GetQuotaMap(ctx, fs)
	if err != nil {
		t.Fatalf("GetQuotaMap: %v", err)
	}
	q := qm.GetQuotaForInodeId(inode)
	if q == nil {
		t.Fatalf("quota map has no entry for inode %d (map holds %d)", inode, qm.Len())
	}
	if q.HardLimitBytes != hard {
		t.Errorf("HardLimitBytes = %d, want %d", q.HardLimitBytes, hard)
	}
	if q.UsedBytes != used {
		t.Errorf("UsedBytes = %d, want %d - the listing reports consumed capacity as total_bytes, "+
			"which GetQuotaMap must translate onto Quota.UsedBytes", q.UsedBytes, used)
	}
}
