package wekafs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePodMountAnnotation(t *testing.T) {
	annotation := `
my-volume-.*: -forcedirect, +readcache
my-vol-1: -forcedirect, +readcache, +writecache
my-vol-2: -forcedirect
my-vol-3: +inode_bits=64
`
	entries := parsePodMountAnnotation(annotation)
	require.Len(t, entries, 4)

	assert.True(t, entries[0].pattern.MatchString("my-volume-dev"))
	assert.True(t, entries[0].pattern.MatchString("my-volume-anything"))
	assert.False(t, entries[0].pattern.MatchString("not-my-volume"))
	assert.Equal(t, "-forcedirect, +readcache", entries[0].rawOpts)

	assert.True(t, entries[1].pattern.MatchString("my-vol-1"))
	assert.Equal(t, "-forcedirect, +readcache, +writecache", entries[1].rawOpts)

	assert.True(t, entries[3].pattern.MatchString("my-vol-3"))
	assert.Equal(t, "+inode_bits=64", entries[3].rawOpts)
}

func TestParsePodMountAnnotationSemicolon(t *testing.T) {
	annotation := "my-volume-.*: -forcedirect; my-vol-1: -forcedirect"
	entries := parsePodMountAnnotation(annotation)
	require.Len(t, entries, 2)
	assert.True(t, entries[0].pattern.MatchString("my-volume-dev"))
	assert.True(t, entries[1].pattern.MatchString("my-vol-1"))
}

func TestParsePodMountAnnotationIgnoresComments(t *testing.T) {
	annotation := `
# this is a comment
foo-.*: -forcedirect
`
	entries := parsePodMountAnnotation(annotation)
	require.Len(t, entries, 1)
}

func TestParsePodMountAnnotationInvalidRegex(t *testing.T) {
	annotation := `[invalid: -forcedirect
valid-pvc: +readcache`
	entries := parsePodMountAnnotation(annotation)
	// Invalid regex is skipped, only valid entry is returned
	require.Len(t, entries, 1)
	assert.True(t, entries[0].pattern.MatchString("valid-pvc"))
}

func TestApplyAnnotationMountOptions_Add(t *testing.T) {
	exclusives := []mutuallyExclusiveMountOptionSet{
		{MountOptionWriteCache, MountOptionCoherent, MountOptionReadCache},
	}
	base := NewMountOptionsFromString("writecache,sync_on_close")
	result := applyAnnotationMountOptions(base, "+forcedirect", exclusives)
	assert.True(t, result.hasOption("forcedirect"))
	assert.True(t, result.hasOption("writecache"))
}

func TestApplyAnnotationMountOptions_Remove(t *testing.T) {
	exclusives := []mutuallyExclusiveMountOptionSet{}
	base := NewMountOptionsFromString("writecache,forcedirect,sync_on_close")
	result := applyAnnotationMountOptions(base, "-forcedirect", exclusives)
	assert.False(t, result.hasOption("forcedirect"))
	assert.True(t, result.hasOption("writecache"))
}

func TestApplyAnnotationMountOptions_MutuallyExclusive(t *testing.T) {
	exclusives := []mutuallyExclusiveMountOptionSet{
		{MountOptionWriteCache, MountOptionCoherent, MountOptionReadCache},
	}
	base := NewMountOptionsFromString("writecache")
	// +readcache should replace writecache (mutually exclusive)
	result := applyAnnotationMountOptions(base, "+readcache", exclusives)
	assert.True(t, result.hasOption("readcache"))
	assert.False(t, result.hasOption("writecache"))
}

func TestApplyAnnotationMountOptions_WithValue(t *testing.T) {
	exclusives := []mutuallyExclusiveMountOptionSet{}
	base := NewMountOptionsFromString("writecache")
	result := applyAnnotationMountOptions(base, "+inode_bits=64", exclusives)
	assert.True(t, result.hasOption("inode_bits"))
	assert.Equal(t, "64", result.getOptionValue("inode_bits"))
}

func TestExtractPVNameFromTargetPath(t *testing.T) {
	path := "/var/lib/kubelet/pods/abc-123/volumes/kubernetes.io~csi/pvc-xyz/mount"
	assert.Equal(t, "pvc-xyz", extractPVNameFromTargetPath(path))
}

func TestExtractPVNameFromTargetPath_NoMatch(t *testing.T) {
	assert.Equal(t, "", extractPVNameFromTargetPath("/var/lib/kubelet/pods/abc/volumes/nfs/pvc-xyz/mount"))
	assert.Equal(t, "", extractPVNameFromTargetPath(""))
}
