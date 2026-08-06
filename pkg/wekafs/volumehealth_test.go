package wekafs

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPreferredSecretRef(t *testing.T) {
	ref := func(name string) *v1.SecretReference {
		return &v1.SecretReference{Name: name, Namespace: "csi-wekafs"}
	}

	for _, tc := range []struct {
		name     string
		source   *v1.CSIPersistentVolumeSource
		expected string
	}{
		{
			name:   "no CSI source",
			source: nil,
		},
		{
			name:   "no refs at all",
			source: &v1.CSIPersistentVolumeSource{Driver: "csi.weka.io"},
		},
		{
			name: "controller expand ref wins over node refs",
			source: &v1.CSIPersistentVolumeSource{
				ControllerExpandSecretRef:  ref("expand"),
				ControllerPublishSecretRef: ref("publish"),
				NodeStageSecretRef:         ref("stage"),
			},
			expected: "expand",
		},
		{
			name: "falls back to node stage ref",
			source: &v1.CSIPersistentVolumeSource{
				NodeStageSecretRef:   ref("stage"),
				NodePublishSecretRef: ref("node-publish"),
			},
			expected: "stage",
		},
		{
			name: "ref without namespace is unusable",
			source: &v1.CSIPersistentVolumeSource{
				ControllerExpandSecretRef: &v1.SecretReference{Name: "expand"},
				NodeStageSecretRef:        ref("stage"),
			},
			expected: "stage",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pv := &v1.PersistentVolume{
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeSource: v1.PersistentVolumeSource{CSI: tc.source},
				},
			}
			got := preferredSecretRef(pv)
			if tc.expected == "" {
				if got != nil {
					t.Fatalf("expected no secret ref, got %s/%s", got.Namespace, got.Name)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected secret ref %s, got none", tc.expected)
			}
			if got.Name != tc.expected {
				t.Fatalf("expected secret ref %s, got %s", tc.expected, got.Name)
			}
		})
	}
}

func TestPvCapacityBytes(t *testing.T) {
	if got := pvCapacityBytes(&v1.PersistentVolume{}); got != 0 {
		t.Fatalf("expected 0 for PV without capacity, got %d", got)
	}
	pv := &v1.PersistentVolume{
		Spec: v1.PersistentVolumeSpec{
			Capacity: v1.ResourceList{v1.ResourceStorage: resource.MustParse("1Gi")},
		},
	}
	if got := pvCapacityBytes(pv); got != 1024*1024*1024 {
		t.Fatalf("expected 1Gi in bytes, got %d", got)
	}
}

func TestSecretCache(t *testing.T) {
	sc := newSecretCache(time.Hour)
	if _, ok := sc.lookup("csi-wekafs/api"); ok {
		t.Fatal("expected a miss on an empty cache")
	}

	sc.store("csi-wekafs/api", map[string]string{"username": "admin"})
	secrets, ok := sc.lookup("csi-wekafs/api")
	if !ok {
		t.Fatal("expected a hit for a freshly stored key")
	}
	if secrets["username"] != "admin" {
		t.Fatalf("unexpected secret contents: %v", secrets)
	}
	if _, ok := sc.lookup("csi-wekafs/other"); ok {
		t.Fatal("expected a miss for a key that was never stored")
	}

	// A zero TTL makes every entry stale on arrival, so every lookup is a miss.
	expired := newSecretCache(0)
	expired.store("csi-wekafs/api", map[string]string{"username": "admin"})
	if _, ok := expired.lookup("csi-wekafs/api"); ok {
		t.Fatal("expected a miss for a stale entry")
	}
}

func TestAbnormalVolumeHealth(t *testing.T) {
	health := abnormalVolumeHealth("filesystem %s does not exist", "myfs")
	if !health.Abnormal {
		t.Fatal("expected health to be abnormal")
	}
	if health.Message != "filesystem myfs does not exist" {
		t.Fatalf("unexpected message: %s", health.Message)
	}
	if health.Capacity != 0 {
		t.Fatalf("expected no capacity on an abnormal volume, got %d", health.Capacity)
	}
}

func TestListVolumesTokenRoundTrip(t *testing.T) {
	handle := "dir/v1/default/csi-volumes/pvc-abc-0123456789"
	got, err := decodeListVolumesToken(encodeListVolumesToken(handle))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != handle {
		t.Fatalf("expected %q, got %q", handle, got)
	}

	// An empty token means "start from the beginning", not an error.
	if cursor, err := decodeListVolumesToken(""); err != nil || cursor != "" {
		t.Fatalf("expected empty cursor with no error, got %q / %v", cursor, err)
	}
}

func TestListVolumesTokenRejectsForeignTokens(t *testing.T) {
	// The CSI spec requires an unrecognized starting_token to be reported as ABORTED, so every
	// token this driver did not mint must be detectable rather than read as a position.
	for _, token := range []string{
		"not base64 at all!!",
		"!!!!",
		base64.RawURLEncoding.EncodeToString([]byte("someoneelse:v1:handle")),
		base64.RawURLEncoding.EncodeToString([]byte("42")),
		base64.RawURLEncoding.EncodeToString([]byte("wekafs:v2:handle")),
	} {
		if _, err := decodeListVolumesToken(token); err == nil {
			t.Fatalf("expected token %q to be rejected", token)
		}
	}
}

func pvWithHandle(name, driver, handle string) v1.PersistentVolume {
	return v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CSI: &v1.CSIPersistentVolumeSource{Driver: driver, VolumeHandle: handle},
			},
		},
	}
}

func TestVolumesAfterHandleFiltersAndOrders(t *testing.T) {
	items := []v1.PersistentVolume{
		pvWithHandle("c", "csi.weka.io", "dir/v1/fs/c"),
		pvWithHandle("a", "csi.weka.io", "dir/v1/fs/a"),
		pvWithHandle("other", "csi.other.io", "dir/v1/fs/b"),
		pvWithHandle("nocsi", "", ""),
		pvWithHandle("b", "csi.weka.io", "dir/v1/fs/b"),
	}
	items[3].Spec.CSI = nil

	got := volumesAfterHandle(items, "csi.weka.io", "")
	if len(got) != 3 {
		t.Fatalf("expected 3 volumes of our driver, got %d", len(got))
	}
	for i, want := range []string{"dir/v1/fs/a", "dir/v1/fs/b", "dir/v1/fs/c"} {
		if got[i].Spec.CSI.VolumeHandle != want {
			t.Fatalf("position %d: expected %s, got %s", i, want, got[i].Spec.CSI.VolumeHandle)
		}
	}

	// The cursor is exclusive.
	got = volumesAfterHandle(items, "csi.weka.io", "dir/v1/fs/a")
	if len(got) != 2 || got[0].Spec.CSI.VolumeHandle != "dir/v1/fs/b" {
		t.Fatalf("expected to resume after the cursor, got %d entries starting at %s", len(got), got[0].Spec.CSI.VolumeHandle)
	}
	if len(volumesAfterHandle(items, "csi.weka.io", "dir/v1/fs/z")) != 0 {
		t.Fatal("expected no entries past the last handle")
	}
}

// TestListVolumesPaginationCoversEverything is the property that matters: paging with a keyset
// cursor must visit every volume exactly once even while volumes are deleted mid-listing, which
// is exactly where an offset-based token silently skips entries.
func TestListVolumesPaginationCoversEverything(t *testing.T) {
	var items []v1.PersistentVolume
	for i := 0; i < 25; i++ {
		h := fmt.Sprintf("dir/v1/fs/vol-%02d", i)
		items = append(items, pvWithHandle(h, "csi.weka.io", h))
	}

	const pageSize = 4
	seen := map[string]int{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 50 {
			t.Fatal("pagination did not terminate")
		}
		// Drive the same two functions ListVolumes itself uses, so this exercises the real paging
		// logic rather than a copy of it.
		remaining := volumesAfterHandle(items, "csi.weka.io", cursor)
		if len(remaining) == 0 {
			break
		}
		page, nextToken := paginateVolumes(remaining, pageSize)
		for _, pv := range page {
			seen[pv.Spec.CSI.VolumeHandle]++
		}
		if nextToken == "" {
			break
		}
		var err error
		if cursor, err = decodeListVolumesToken(nextToken); err != nil {
			t.Fatalf("driver minted a token it cannot decode: %v", err)
		}

		// Delete an already-listed volume between pages. With an offset token this shifts the
		// remaining entries and one volume is never returned; with a cursor it cannot.
		if len(items) > 1 {
			items = items[1:]
		}
	}

	for i := 0; i < 25; i++ {
		h := fmt.Sprintf("dir/v1/fs/vol-%02d", i)
		if seen[h] != 1 {
			t.Fatalf("volume %s was returned %d times, expected exactly 1", h, seen[h])
		}
	}
}

func TestFilesystemCache(t *testing.T) {
	// A nil cache must always miss and must tolerate writes, so single-volume callers can pass nil.
	var absent *filesystemCache
	if absent.get("default") != nil {
		t.Fatal("expected a nil cache to miss")
	}
	absent.put("default", &apiclient.FileSystem{Name: "default"})

	fc := newFilesystemCache()
	if fc.get("default") != nil {
		t.Fatal("expected a miss on an empty cache")
	}
	fs := &apiclient.FileSystem{Name: "default"}
	fc.put("default", fs)
	if got := fc.get("default"); got != fs {
		t.Fatalf("expected the cached filesystem back, got %v", got)
	}
	if fc.get("other") != nil {
		t.Fatal("expected a miss for a filesystem that was never cached")
	}

	// Storing nil must not create an entry that would later be mistaken for a hit.
	fc.put("nilfs", nil)
	if _, ok := fc.byName["nilfs"]; ok {
		t.Fatal("expected a nil filesystem not to be cached")
	}
}
