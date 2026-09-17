package wekafs

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// The extended-attribute machinery is gone, not merely unreferenced. This walks the package source
// so that reintroducing a read or write of the capacity attribute fails here, rather than quietly
// restoring a mechanism that records a limit without enforcing it.
func TestNoExtendedAttributeCapacityCodeRemains(t *testing.T) {
	for _, gone := range []string{
		"user.weka_capacity",
		"xattrCapacity",
		"getSizeFromXattr",
		"updateCapacityXattr",
		"setVolumeProperties",
		"updateXattrs",
		"github.com/pkg/xattr",
	} {
		assert.False(t, packageSourceContains(t, gone),
			"%q must not reappear: capacity belongs in the quota only", gone)
	}
}

// Removing the fallback turned two version gates from "degrade quietly" into "refuse", and the
// refusal messages quote these minimums. An unset field would render as an empty version and make
// the error meaningless - which is exactly how the unused QuotaOnNonEmptyDirs field read before it
// was deleted.
func TestRefusalMessagesQuoteRealMinimumVersions(t *testing.T) {
	for name, v := range map[string]string{
		"QuotaDirectoryAsVolume":         apiclient.MinimumSupportedWekaVersions.QuotaDirectoryAsVolume,
		"MountFilesystemsUsingAuthToken": apiclient.MinimumSupportedWekaVersions.MountFilesystemsUsingAuthToken,
	} {
		assert.NotEmpty(t, v, "%s is quoted in a user-facing refusal and must have a value", name)
		assert.True(t, strings.HasPrefix(v, "v"), "%s should read as a version, got %q", name, v)
	}
}

// packageSourceContains reports whether any non-test Go file in this package mentions needle.
func packageSourceContains(t *testing.T, needle string) bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("could not read package directory: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("could not read %s: %v", name, err)
		}
		if strings.Contains(string(b), needle) {
			t.Logf("found %q in %s", needle, name)
			return true
		}
	}
	return false
}
