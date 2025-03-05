package wekafs

import (
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
	opts1 := NewMountOptions([]string{"ro"})
	opts2 := NewMountOptions([]string{"rw"})

	exclusives := []mutuallyExclusiveMountOptionSet{
		{"ro", "rw"},
	}

	opts1.Merge(opts2, exclusives)
	opts1.AddOption("acl")

	if opts1.hasOption("ro") {
		t.Errorf("Expected option 'ro' to be removed due to exclusivity")
	}

	if !opts1.hasOption("rw") {
		t.Errorf("Expected option 'rw' to be added")
	}

	if !opts1.hasOption("acl") {
		t.Errorf("Expected option 'acl' to be added")
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
