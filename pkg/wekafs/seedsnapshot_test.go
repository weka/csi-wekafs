package wekafs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDirHoldsOnlySnapshots covers the decision both isFilesystemEmpty and isSeedSnapshotEmpty rest
// on. The wrappers themselves need a mounted filesystem, so this exercises the part that decides
// whether a seed snapshot is safe to provision from.
func TestDirHoldsOnlySnapshots(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		dirs    []string
		want    bool
	}{
		{name: "empty directory", want: true},
		{name: "only the snapshots directory", dirs: []string{SnapshotsSubDirectory}, want: true},
		{name: "a single data file", entries: []string{"payload"}, want: false},
		{name: "snapshots directory alongside data", dirs: []string{SnapshotsSubDirectory}, entries: []string{"payload"}, want: false},
		{name: "a data directory", dirs: []string{"somedir"}, want: false},
		// More entries than the two names read, to confirm the answer does not depend on which two
		// the filesystem happens to return first.
		{name: "many data files", entries: []string{"a", "b", "c", "d", "e"}, want: false},
		{name: "many data files beside snapshots", dirs: []string{SnapshotsSubDirectory}, entries: []string{"a", "b", "c", "d", "e"}, want: false},
		// A dotfile is data like any other; only SnapshotsSubDirectory is exempt.
		{name: "an unrelated dotfile", entries: []string{".hidden"}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, d := range tc.dirs {
				require.NoError(t, os.Mkdir(filepath.Join(root, d), 0o750))
			}
			for _, f := range tc.entries {
				require.NoError(t, os.WriteFile(filepath.Join(root, f), []byte("x"), 0o600))
			}

			dir, err := os.Open(root)
			require.NoError(t, err)
			defer func() { _ = dir.Close() }()

			got, err := dirHoldsOnlySnapshots(dir)
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDirHoldsOnlySnapshotsFailsClosed pins the behaviour that matters most: a directory that
// cannot be read must report an error rather than "empty". Answering "empty" here would wave
// through the exact case the seed snapshot check exists to catch.
func TestDirHoldsOnlySnapshotsFailsClosed(t *testing.T) {
	root := t.TempDir()
	dir, err := os.Open(root)
	require.NoError(t, err)
	require.NoError(t, dir.Close()) // reading from it now fails

	empty, err := dirHoldsOnlySnapshots(dir)
	assert.Error(t, err, "a directory that cannot be read must not be reported as empty")
	assert.False(t, empty)
}
