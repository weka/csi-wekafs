package volumeid

import "testing"

// handles covers every shape the driver emits, including the non-normalised separators
// produced when dynamicVolPath is empty or slash-prefixed.
var handles = []struct {
	name        string
	handle      string
	volType     Type
	filesystem  string
	accessPoint string
	innerPath   string
	backing     Backing
	portable    bool
}{
	{
		name:       "filesystem backed",
		handle:     "weka/v2/csivol-my-test-volu-97ab4a2a2b6d",
		volType:    TypeUnified,
		filesystem: "csivol-my-test-volu-97ab4a2a2b6d",
		backing:    BackingFilesystem,
		portable:   true,
	},
	{
		name:       "directory backed",
		handle:     "weka/v2/testfs/csi-volumes/vol-16a0fb48706cbd9445856412ef99589b85e5e03c",
		volType:    TypeUnified,
		filesystem: "testfs",
		innerPath:  "/csi-volumes/vol-16a0fb48706cbd9445856412ef99589b85e5e03c",
		backing:    BackingDirectory,
		portable:   true,
	},
	{
		name:       "directory backed with doubled separator",
		handle:     "weka/v2/testfs//vol-16a0fb48706cbd9445856412ef99589b85e5e03c",
		volType:    TypeUnified,
		filesystem: "testfs",
		innerPath:  "//vol-16a0fb48706cbd9445856412ef99589b85e5e03c",
		backing:    BackingDirectory,
		portable:   true,
	},
	{
		name:       "legacy directory backed",
		handle:     "dir/v1/testfs/testdir",
		volType:    TypeDirV1,
		filesystem: "testfs",
		innerPath:  "/testdir",
		backing:    BackingDirectory,
		portable:   true,
	},
	{
		name:        "snapshot backed",
		handle:      "weka/v2/csi-volsgen2:my-test-volu-97ab4a2a2b6d",
		volType:     TypeUnified,
		filesystem:  "csi-volsgen2",
		accessPoint: "my-test-volu-97ab4a2a2b6d",
		backing:     BackingSnapshot,
		portable:    false,
	},
	{
		name:        "directory on snapshot",
		handle:      "weka/v2/csi-volsgen2:my-test-volu-97ab4a2a2b6d/csi-volumes/my-test-volume-97ab4a2a",
		volType:     TypeUnified,
		filesystem:  "csi-volsgen2",
		accessPoint: "my-test-volu-97ab4a2a2b6d",
		innerPath:   "/csi-volumes/my-test-volume-97ab4a2a",
		backing:     BackingSnapshotDirectory,
		portable:    false,
	},
}

func TestParse(t *testing.T) {
	for _, tc := range handles {
		t.Run(tc.name, func(t *testing.T) {
			h, err := Parse(tc.handle)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.handle, err)
			}
			if h.Type != tc.volType {
				t.Errorf("Type = %q, want %q", h.Type, tc.volType)
			}
			if h.FilesystemName != tc.filesystem {
				t.Errorf("FilesystemName = %q, want %q", h.FilesystemName, tc.filesystem)
			}
			if h.SnapshotAccessPoint != tc.accessPoint {
				t.Errorf("SnapshotAccessPoint = %q, want %q", h.SnapshotAccessPoint, tc.accessPoint)
			}
			if h.InnerPath != tc.innerPath {
				t.Errorf("InnerPath = %q, want %q", h.InnerPath, tc.innerPath)
			}
			if got := h.Backing(); got != tc.backing {
				t.Errorf("Backing() = %q, want %q", got, tc.backing)
			}
			if got := h.PortableAcrossWekaClusters(); got != tc.portable {
				t.Errorf("PortableAcrossWekaClusters() = %v, want %v", got, tc.portable)
			}
		})
	}
}

// TestParseStringIsLossless is the guarantee the migrator depends on: a handle that makes a
// round trip through the parser must come back byte-for-byte identical, or a restored PV
// would address different data than the original.
func TestParseStringIsLossless(t *testing.T) {
	for _, tc := range handles {
		t.Run(tc.name, func(t *testing.T) {
			h, err := Parse(tc.handle)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.handle, err)
			}
			if got := h.String(); got != tc.handle {
				t.Errorf("String() = %q, want %q (handle was mutated by parsing)", got, tc.handle)
			}
		})
	}
}

func TestParseRejectsUnknownHandles(t *testing.T) {
	for _, handle := range []string{
		"",
		"weka/v2",
		"weka/v2/",
		"nfs/v1/testfs/dir",
		"/var/log/messages",
		"testfs",
	} {
		if h, err := Parse(handle); err == nil {
			t.Errorf("Parse(%q) unexpectedly succeeded, returning %+v", handle, h)
		}
	}
}

func TestWithFilesystemNamePreservesEverythingElse(t *testing.T) {
	for _, tc := range []struct {
		handle string
		want   string
	}{
		{"weka/v2/testfs", "weka/v2/renamed"},
		{"weka/v2/testfs/csi-volumes/vol-abc", "weka/v2/renamed/csi-volumes/vol-abc"},
		{"weka/v2/testfs//vol-abc", "weka/v2/renamed//vol-abc"},
		{"dir/v1/testfs/testdir", "dir/v1/renamed/testdir"},
		{"weka/v2/testfs:snap-abc/inner", "weka/v2/renamed:snap-abc/inner"},
	} {
		h, err := Parse(tc.handle)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", tc.handle, err)
		}
		renamed, err := h.WithFilesystemName("renamed")
		if err != nil {
			t.Fatalf("WithFilesystemName on %q returned error: %v", tc.handle, err)
		}
		if got := renamed.String(); got != tc.want {
			t.Errorf("WithFilesystemName(%q) = %q, want %q", tc.handle, got, tc.want)
		}
	}
}

func TestWithFilesystemNameRejectsSeparators(t *testing.T) {
	h, err := Parse("weka/v2/testfs/inner")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	for _, name := range []string{"", "with/slash", "with:colon"} {
		if _, err := h.WithFilesystemName(name); err == nil {
			t.Errorf("WithFilesystemName(%q) unexpectedly succeeded", name)
		}
	}
}

// TestSlicersMatchParse pins the standalone slicers to the parser so the driver, which uses
// the slicers, and the migrator, which uses Parse, can never disagree about a handle.
func TestSlicersMatchParse(t *testing.T) {
	for _, tc := range handles {
		t.Run(tc.name, func(t *testing.T) {
			if got := SliceType(tc.handle); got != tc.volType {
				t.Errorf("SliceType() = %q, want %q", got, tc.volType)
			}
			if got := SliceFilesystemName(tc.handle); got != tc.filesystem {
				t.Errorf("SliceFilesystemName() = %q, want %q", got, tc.filesystem)
			}
			if got := SliceSnapshotAccessPoint(tc.handle); got != tc.accessPoint {
				t.Errorf("SliceSnapshotAccessPoint() = %q, want %q", got, tc.accessPoint)
			}
			if got := SliceInnerPath(tc.handle); got != tc.innerPath {
				t.Errorf("SliceInnerPath() = %q, want %q", got, tc.innerPath)
			}
		})
	}
}

func TestBuildMatchesDriverSeparatorRules(t *testing.T) {
	// A leading slash on innerPath yields a doubled separator, exactly as the driver does.
	if got := Build(TypeUnified, "fs1", "", "/foo"); got != "weka/v2/fs1//foo" {
		t.Errorf("Build with slash-prefixed innerPath = %q, want %q", got, "weka/v2/fs1//foo")
	}
	if got := Build(TypeUnified, "fs1", "", "csi-volumes/foo"); got != "weka/v2/fs1/csi-volumes/foo" {
		t.Errorf("Build = %q, want %q", got, "weka/v2/fs1/csi-volumes/foo")
	}
	if got := Build(TypeUnified, "fs1", "", ""); got != "weka/v2/fs1" {
		t.Errorf("Build = %q, want %q", got, "weka/v2/fs1")
	}
	if got := Build(TypeUnified, "fs1", "ap1", ""); got != "weka/v2/fs1:ap1" {
		t.Errorf("Build = %q, want %q", got, "weka/v2/fs1:ap1")
	}
}
