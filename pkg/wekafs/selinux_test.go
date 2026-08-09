package wekafs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// SELinux state must come from the kernel, not from configuration.
//
// The case that made this necessary is "config file says enforcing": the plugin image installs
// container-selinux and therefore ships its own /etc/selinux/config saying SELINUX=enforcing, so
// reading that file from inside the container reported enforcing on every cluster - including hosts
// with no SELinux at all - and put an fscontext on every mount.
func TestSelinuxStatusComesFromTheKernel(t *testing.T) {
	ctx := context.Background()

	for name, tc := range map[string]struct {
		enforceContents string // "" means the path does not exist, as on a host without selinuxfs
		want            bool
	}{
		"enforcing":                     {"1", true},
		"enforcing with trailing space": {"1\n", true},
		// Permissive still labels, so a mount made without a context would be labelled wrong and
		// start failing the moment the host is switched to enforcing.
		"permissive":                 {"0", true},
		"no selinuxfs on this host":  {"", false},
		"unreadable/unexpected byte": {"banana", false},
		"empty file":                 {" ", false},
	} {
		t.Run(name, func(t *testing.T) {
			enforcePath := filepath.Join(t.TempDir(), "enforce")
			if tc.enforceContents != "" {
				if err := os.WriteFile(enforcePath, []byte(tc.enforceContents), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := selinuxEnabledAt(ctx, enforcePath); got != tc.want {
				t.Errorf("selinuxEnabledAt(%q) = %v, want %v", tc.enforceContents, got, tc.want)
			}
		})
	}
}

// The old implementation read this file, and the image ships one saying enforcing. Its presence must
// no longer influence the answer.
func TestSelinuxConfigFileIsNotConsulted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte("SELINUX=enforcing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No enforce file alongside it: the kernel has no SELinux, whatever the config claims.
	if selinuxEnabledAt(context.Background(), filepath.Join(dir, "enforce")) {
		t.Error("reported SELinux enabled with no selinuxfs present - configuration is being trusted over the kernel")
	}
}
