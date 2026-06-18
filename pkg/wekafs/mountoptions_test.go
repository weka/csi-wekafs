package wekafs

import (
	"strings"
	"testing"
)

func TestMountOptions_AddOption(t *testing.T) {
	opts := NewMountOptions([]string{})
	opts = opts.AddOption("ro")

	if !opts.hasOption("ro") {
		t.Errorf("Expected option 'ro' to be added")
	}
}

func TestMountOptions_RemoveOption(t *testing.T) {
	opts := NewMountOptions([]string{"ro"})
	opts = opts.RemoveOption("ro")

	if opts.hasOption("ro") {
		t.Errorf("Expected option 'ro' to be removed")
	}
}

func TestMountOptions_Merge(t *testing.T) {
	opts1 := NewMountOptions([]string{"ro", "writecache"})
	opts2 := NewMountOptions([]string{"rw", "forcedirect"})

	exclusives := []mutuallyExclusiveMountOptionSet{
		{"ro", "rw"},
		{"coherent", "forcedirect", "readcache", "writecache"},
	}

	opts1.Merge(opts2, exclusives)
	opts1 = opts1.AddOption("acl")

	if opts1.hasOption("ro") {
		t.Errorf("Expected option 'ro' to be removed due to exclusivity")
	}

	if !opts1.hasOption("rw") {
		t.Errorf("Expected option 'rw' to be added")
	}

	if !opts1.hasOption("acl") {
		t.Errorf("Expected option 'acl' to be added")
	}

	if !opts1.hasOption("forcedirect") {
		t.Errorf("Expected option 'forcedirect' to be added")
	}

	if opts1.hasOption("writecache") {
		t.Errorf("Expected option 'writecache' to be removed due to exclusivity")
	}
}

func TestMountOptions_Strings(t *testing.T) {
	opts := NewMountOptions([]string{"ro", "sync_on_close"})
	expected := "ro,sync_on_close"
	result := opts.String()

	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestMountOptions_Hash(t *testing.T) {
	opts := NewMountOptions([]string{"ro", "sync_on_close"})
	hash := opts.Hash()

	if hash == 0 {
		t.Errorf("Expected non-zero hash value")
	}
}

func TestMountOptions_AsMapKey(t *testing.T) {
	opts := NewMountOptions([]string{"ro", "sync_on_close"})
	mapKey := opts.AsMapKey()

	if mapKey == "" {
		t.Errorf("Expected non-empty map key")
	}
}

func TestMountOptions_setSelinux(t *testing.T) {
	opts := NewMountOptions([]string{})
	opts.setSelinux(true, MountProtocolWekafs)

	if !opts.hasOption("fscontext=\"system_u:object_r:wekafs_csi_volume_t:s0\"") {
		t.Errorf("Expected SELinux context to be set for WekaFS")
	}

	opts.setSelinux(false, MountProtocolWekafs)

	if opts.hasOption("fscontext") {
		t.Errorf("Expected SELinux context to be removed for WekaFS")
	}
}

func TestMountOptions_AsNfs(t *testing.T) {
	opts1 := NewMountOptions([]string{"ro", "sync_on_close"})
	opts2 := NewMountOptions([]string{"coherent", "sync_on_close"})
	opts3 := NewMountOptions([]string{"forcedirect", "sync_on_close"})
	opts4 := NewMountOptions([]string{"readcache", "writecache", "sync_on_close"})
	opts5 := NewMountOptions([]string{"dentry_max_age_positive=10", "sync_on_close"})
	opts6 := NewMountOptions([]string{})

	opts := opts1.AsNfs()

	if opts.hasOption("ro") {
		t.Errorf("Expected option 'ro' to be removed")
	}

	if opts.hasOption("sync_on_close") {
		t.Errorf("Expected option 'sync_on_close' to be removed")
	}

	opts = opts2.AsNfs()
	if opts.hasOption("coherent") {
		t.Errorf("Expected option 'coherent' to be removed")
	}
	if !opts.hasOption("sync") {
		t.Errorf("Expected option 'sync' to be added")
	}

	opts = opts3.AsNfs()
	if opts.hasOption("forcedirect") {
		t.Errorf("Expected option 'forcedirect' to be removed")
	}
	if !opts.hasOption("sync") {
		t.Errorf("Expected option 'sync' to be added")
	}

	opts = opts4.AsNfs()
	if opts.hasOption("writecache") {
		t.Errorf("Expected option 'writecache' to be removed")
	}
	if !opts.hasOption("async") {
		t.Errorf("Expected option 'async' to be added")
	}

	opts = opts5.AsNfs()
	if opts.hasOption("dentry_max_age_positive") {
		t.Errorf("Expected option 'dentry_max_age_positive' to be removed")
	}
	if !opts.hasOption("acdirmax") {
		t.Errorf("Expected option 'acdirmax' to be added")
	}
	if !opts.hasOption("acregmax") {
		t.Errorf("Expected option 'acregmax' to be added")
	}
	if opts.getOptionValue("acdirmax") != "10" {
		t.Errorf("Expected option 'acdirmax' to have value 10")
	}

	opts = opts6.AsNfs()
	if !opts.hasOption("async") {
		t.Errorf("Expected option 'async' to be added")
	}
}

// Tests for MountOptionOverride functionality
func TestMountOptionOverride_ApplyToOptions_AddOption(t *testing.T) {
	opts := NewMountOptions([]string{"ro"})
	exclusives := []mutuallyExclusiveMountOptionSet{
		{"ro", "rw"},
	}

	override := MountOptionOverride("+readcache")
	result := override.ApplyToOptions(opts, exclusives)

	if !result.hasOption("readcache") {
		t.Errorf("Expected 'readcache' to be added with + prefix")
	}
	if !result.hasOption("ro") {
		t.Errorf("Expected 'ro' to remain")
	}
}

func TestMountOptionOverride_ApplyToOptions_RemoveOption(t *testing.T) {
	opts := NewMountOptions([]string{"ro", "readcache"})
	exclusives := []mutuallyExclusiveMountOptionSet{
		{"ro", "rw"},
	}

	override := MountOptionOverride("-readcache")
	result := override.ApplyToOptions(opts, exclusives)

	if result.hasOption("readcache") {
		t.Errorf("Expected 'readcache' to be removed with - prefix")
	}
	if !result.hasOption("ro") {
		t.Errorf("Expected 'ro' to remain")
	}
}

func TestMountOptionOverride_ApplyToOptions_AddWithoutPrefix(t *testing.T) {
	opts := NewMountOptions([]string{})
	exclusives := []mutuallyExclusiveMountOptionSet{}

	override := MountOptionOverride("noatime")
	result := override.ApplyToOptions(opts, exclusives)

	if !result.hasOption("noatime") {
		t.Errorf("Expected 'noatime' to be added without prefix")
	}
}

func TestMountOptionOverride_ApplyToOptions_MultipleModifiers(t *testing.T) {
	opts := NewMountOptions([]string{"forcedirect"})
	exclusives := []mutuallyExclusiveMountOptionSet{
		{"coherent", "forcedirect", "readcache", "writecache"},
	}

	override := MountOptionOverride("-forcedirect, +readcache, +noatime")
	result := override.ApplyToOptions(opts, exclusives)

	if result.hasOption("forcedirect") {
		t.Errorf("Expected 'forcedirect' to be removed")
	}
	if !result.hasOption("readcache") {
		t.Errorf("Expected 'readcache' to be added")
	}
	if !result.hasOption("noatime") {
		t.Errorf("Expected 'noatime' to be added")
	}
}

func TestMountOptionOverride_ApplyToOptions_WithMutuallyExclusive(t *testing.T) {
	opts := NewMountOptions([]string{"writecache", "ro"})
	exclusives := []mutuallyExclusiveMountOptionSet{
		{"ro", "rw"},
		{"coherent", "forcedirect", "readcache", "writecache"},
	}

	override := MountOptionOverride("+readcache, +rw")
	result := override.ApplyToOptions(opts, exclusives)

	if !result.hasOption("readcache") {
		t.Errorf("Expected 'readcache' to be added")
	}
	if result.hasOption("writecache") {
		t.Errorf("Expected 'writecache' to be removed due to exclusivity with readcache")
	}
	if !result.hasOption("rw") {
		t.Errorf("Expected 'rw' to be added")
	}
	if result.hasOption("ro") {
		t.Errorf("Expected 'ro' to be removed due to exclusivity with rw")
	}
}

func TestMountOptionOverride_ApplyToOptions_WithValuedOption(t *testing.T) {
	opts := NewMountOptions([]string{})
	exclusives := []mutuallyExclusiveMountOptionSet{}

	override := MountOptionOverride("readahead_kb=32768, dentry_max_age_positive=1000")
	result := override.ApplyToOptions(opts, exclusives)

	if !result.hasOption("readahead_kb") {
		t.Errorf("Expected 'readahead_kb' to be added")
	}
	if result.getOptionValue("readahead_kb") != "32768" {
		t.Errorf("Expected 'readahead_kb' value to be '32768', got '%s'", result.getOptionValue("readahead_kb"))
	}
	if !result.hasOption("dentry_max_age_positive") {
		t.Errorf("Expected 'dentry_max_age_positive' to be added")
	}
	if result.getOptionValue("dentry_max_age_positive") != "1000" {
		t.Errorf("Expected 'dentry_max_age_positive' value to be '1000', got '%s'", result.getOptionValue("dentry_max_age_positive"))
	}
}

func TestMountOptionOverride_ApplyToOptions_EmptyString(t *testing.T) {
	opts := NewMountOptions([]string{"ro"})
	exclusives := []mutuallyExclusiveMountOptionSet{}

	override := MountOptionOverride("")
	result := override.ApplyToOptions(opts, exclusives)

	if !result.hasOption("ro") {
		t.Errorf("Expected 'ro' to remain unchanged with empty override")
	}
}

func TestMountOptionOverride_ApplyToOptions_WithWhitespace(t *testing.T) {
	opts := NewMountOptions([]string{})
	exclusives := []mutuallyExclusiveMountOptionSet{}

	override := MountOptionOverride("  +readcache  ,  -forcedirect  ,  noatime  ")
	result := override.ApplyToOptions(opts, exclusives)

	if !result.hasOption("readcache") {
		t.Errorf("Expected 'readcache' to be added (whitespace handling)")
	}
	if !result.hasOption("noatime") {
		t.Errorf("Expected 'noatime' to be added (whitespace handling)")
	}
}

func TestParsePodMountAnnotation_SingleEntry(t *testing.T) {
	annotation := "my-volume: -forcedirect, +readcache"
	entries := parsePodMountAnnotation(annotation)

	if len(entries) != 1 {
		t.Errorf("Expected 1 entry, got %d", len(entries))
	}

	if !entries[0].pattern.MatchString("my-volume") {
		t.Errorf("Expected pattern to match 'my-volume'")
	}

	if entries[0].override.String() != "-forcedirect, +readcache" {
		t.Errorf("Expected override to be '-forcedirect, +readcache', got '%s'", entries[0].override.String())
	}
}

func TestParsePodMountAnnotation_MultipleEntries_WithNewline(t *testing.T) {
	annotation := "my-volume-.*: -forcedirect, +readcache\nmy-vol-2: +writecache"
	entries := parsePodMountAnnotation(annotation)

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}

	if !entries[0].pattern.MatchString("my-volume-abc") {
		t.Errorf("Expected first pattern to match 'my-volume-abc'")
	}

	if !entries[1].pattern.MatchString("my-vol-2") {
		t.Errorf("Expected second pattern to match 'my-vol-2'")
	}
}

func TestParsePodMountAnnotation_MultipleEntries_WithSemicolon(t *testing.T) {
	annotation := "my-volume: +readcache; other-volume: -writecache"
	entries := parsePodMountAnnotation(annotation)

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}
}

func TestParsePodMountAnnotation_WithComments(t *testing.T) {
	annotation := "# This is a comment\nmy-volume: +readcache\n# Another comment\nother: -forcedirect"
	entries := parsePodMountAnnotation(annotation)

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries (ignoring comments), got %d", len(entries))
	}
}

func TestParsePodMountAnnotation_EmptyLines(t *testing.T) {
	annotation := "my-volume: +readcache\n\nother-volume: -writecache"
	entries := parsePodMountAnnotation(annotation)

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries (ignoring empty lines), got %d", len(entries))
	}
}

func TestParsePodMountAnnotation_InvalidRegex(t *testing.T) {
	annotation := "invalid[: +readcache\nvalid-volume: -writecache"
	entries := parsePodMountAnnotation(annotation)

	// Should only have 1 entry (valid one), the invalid regex is skipped
	if len(entries) != 1 {
		t.Errorf("Expected 1 entry (invalid regex skipped), got %d", len(entries))
	}
}

func TestParsePodMountAnnotation_NoColonSeparator(t *testing.T) {
	annotation := "my-volume -forcedirect, +readcache"
	entries := parsePodMountAnnotation(annotation)

	if len(entries) != 0 {
		t.Errorf("Expected 0 entries (no colon separator), got %d", len(entries))
	}
}

func TestParsePodMountAnnotation_RegexPatterns(t *testing.T) {
	annotation := "cache-.*: +readcache\npvc-[0-9]+: -forcedirect"
	entries := parsePodMountAnnotation(annotation)

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries with regex patterns, got %d", len(entries))
	}

	if !entries[0].pattern.MatchString("cache-volume") {
		t.Errorf("Expected first pattern to match regex")
	}
	if !entries[0].pattern.MatchString("cache-vol-123") {
		t.Errorf("Expected first pattern to match regex with suffix")
	}

	if !entries[1].pattern.MatchString("pvc-123") {
		t.Errorf("Expected second pattern to match 'pvc-123'")
	}
	if entries[1].pattern.MatchString("pvc-abc") {
		t.Errorf("Expected second pattern not to match 'pvc-abc' (not digits)")
	}
}

func TestParsePodMountAnnotation_PatternMatchingPriority(t *testing.T) {
	annotation := "my-.*: +readcache\nmy-volume: +writecache"
	entries := parsePodMountAnnotation(annotation)

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}

	// Both patterns match "my-volume", but first one should be found first
	if !entries[0].pattern.MatchString("my-volume") {
		t.Errorf("Expected first pattern to match 'my-volume'")
	}
	if !entries[1].pattern.MatchString("my-volume") {
		t.Errorf("Expected second pattern to match 'my-volume'")
	}
}

func TestMountOptionOverride_String(t *testing.T) {
	override := MountOptionOverride("test-option")
	if override.String() != "test-option" {
		t.Errorf("Expected String() to return 'test-option', got '%s'", override.String())
	}
}

func TestParsePodMountAnnotation_MixedWhitespace(t *testing.T) {
	annotation := "my-vol-1:  -forcedirect , +readcache , +writecache\nmy-vol-2: +noatime"
	entries := parsePodMountAnnotation(annotation)

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}

	if !entries[0].pattern.MatchString("my-vol-1") {
		t.Errorf("Expected pattern to match 'my-vol-1'")
	}

	// Verify the override string is preserved with spaces
	override := entries[0].override.String()
	parts := strings.Split(override, ",")
	if len(parts) != 3 {
		t.Errorf("Expected 3 parts after split, got %d", len(parts))
	}
}

func TestMountOptionOverride_ApplyToOptions_ComplexScenario(t *testing.T) {
	// Simulating a real-world scenario with multiple mutually exclusive options
	opts := NewMountOptions([]string{"writecache", "forcedirect", "ro"})
	exclusives := []mutuallyExclusiveMountOptionSet{
		{"ro", "rw"},
		{"coherent", "forcedirect", "readcache", "writecache"},
	}

	override := MountOptionOverride("-writecache, -fo, +coherent, +rw, +noatime")
	result := override.ApplyToOptions(opts, exclusives)

	if result.hasOption("writecache") {
		t.Errorf("Expected 'writecache' to be removed")
	}
	if !result.hasOption("coherent") {
		t.Errorf("Expected 'coherent' to be added")
	}
	if result.hasOption("forcedirect") {
		t.Errorf("Expected 'forcedirect' to be removed due to coherent mutual exclusivity")
	}
	if !result.hasOption("rw") {
		t.Errorf("Expected 'rw' to be added")
	}
	if result.hasOption("ro") {
		t.Errorf("Expected 'ro' to be removed due to rw mutual exclusivity")
	}
	if !result.hasOption("noatime") {
		t.Errorf("Expected 'noatime' to be added")
	}
}

// TestMountOptions_AddOption_DoesNotMutateReceiver guards against the
// concurrent-provisioning mount-stacking bug: AddOption must NOT mutate the
// receiver's underlying options map. If it does, callers that use
// MountOptions.String() as a stable identity (e.g. the mount refcount key in
// getRefcountIdx) read one key before doMount adds container_name and write a
// different key after, defeating mount sharing and stacking real mounts.
func TestMountOptions_AddOption_DoesNotMutateReceiver(t *testing.T) {
	original := NewMountOptions([]string{"acl", "writecache"})
	before := original.String()

	derived := original.AddOption("container_name=k8sclient")

	if original.String() != before {
		t.Errorf("AddOption mutated the receiver: before=%q after=%q", before, original.String())
	}
	if original.hasOption("container_name") {
		t.Errorf("AddOption leaked 'container_name' into the receiver")
	}
	if !derived.hasOption("container_name") {
		t.Errorf("AddOption result is missing the added option")
	}
}

// TestMountOptions_RemoveOption_DoesNotMutateReceiver mirrors the AddOption guard.
func TestMountOptions_RemoveOption_DoesNotMutateReceiver(t *testing.T) {
	original := NewMountOptions([]string{"acl", "writecache", "ro"})
	before := original.String()

	derived := original.RemoveOption("ro")

	if original.String() != before {
		t.Errorf("RemoveOption mutated the receiver: before=%q after=%q", before, original.String())
	}
	if !original.hasOption("ro") {
		t.Errorf("RemoveOption removed 'ro' from the receiver")
	}
	if derived.hasOption("ro") {
		t.Errorf("RemoveOption result still has the removed option")
	}
}

// TestMountOptions_RefcountKeyStableAcrossDoMount reproduces the exact refcount-key
// corruption: doMount adds "container_name=..." while mounting. The options string
// used to build the refcount key must be identical before and after that addition,
// so incRef reads and writes the SAME map key and mount sharing works.
func TestMountOptions_RefcountKeyStableAcrossDoMount(t *testing.T) {
	mountOpts := NewMountOptions([]string{"acl", "writecache"})

	keyBefore := mountOpts.String() // what incRef reads at refCount lookup

	// emulate doMount: container_name is added to a derived value for the actual mount
	_ = mountOpts.AddOption("container_name=k8sclient")

	keyAfter := mountOpts.String() // what incRef would write after doMount returns

	if keyBefore != keyAfter {
		t.Fatalf("refcount key changed across doMount: before=%q after=%q (mounts will stack under concurrency)", keyBefore, keyAfter)
	}
}

// TestMountOptions_SetSelinux_StillMutatesInPlace ensures setSelinux keeps its
// in-place contract after the AddOption clone fix (callers rely on it mutating
// the passed options before mounting).
func TestMountOptions_SetSelinux_StillMutatesInPlace(t *testing.T) {
	opts := NewMountOptions([]string{"writecache"})
	opts.setSelinux(true, MountProtocolWekafs)

	if !opts.hasOption("fscontext") {
		t.Errorf("setSelinux did not add fscontext in place")
	}
	if !opts.hasOption(MountOptionAcl) {
		t.Errorf("setSelinux did not add acl in place")
	}
}

// TestMountOptions_AsNfs_TranslatesOptions ensures AsNfs still returns the
// translated NFS options after the AddOption clone fix (it previously relied on
// AddOption mutating in place).
func TestMountOptions_AsNfs_TranslatesOptions(t *testing.T) {
	opts := NewMountOptions([]string{"writecache"})
	nfs := opts.AsNfs()
	if !nfs.hasOption(MountOptionNfsAsync) {
		t.Errorf("AsNfs did not translate writecache -> %s; got %q", MountOptionNfsAsync, nfs.String())
	}
}
