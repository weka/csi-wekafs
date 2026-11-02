package wekafs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rs/zerolog"
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
	if getSelinuxStatus(context.Background(), dir) {
		t.Error("reported SELinux enabled with no selinuxfs present - configuration is being trusted over the kernel")
	}
}

// fakeSelinuxContextWrite makes the selinuxfs context write return err, as the kernel would, and
// restores the real writer when the test ends.
func fakeSelinuxContextWrite(t *testing.T, err error) {
	t.Helper()
	orig := writeSelinuxContext
	writeSelinuxContext = func(contextPath, seContext string) error {
		if seContext != "system_u:object_r:wekafs_csi_volume_t:s0" {
			t.Errorf("validated context %q, want the wekafs volume context", seContext)
		}
		return err
	}
	t.Cleanup(func() { writeSelinuxContext = orig })
}

func TestSelinuxContextValidAt(t *testing.T) {
	ctx := context.Background()

	t.Run("no selinuxfs", func(t *testing.T) {
		valid, err := selinuxContextValidAt(ctx, filepath.Join(t.TempDir(), "missing"), wekafsSelinuxContext)
		if valid || err != nil {
			t.Errorf("got (%v, %v), want (false, nil)", valid, err)
		}
	})
	t.Run("write accepted", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "context"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		valid, err := selinuxContextValidAt(ctx, dir, wekafsSelinuxContext)
		if !valid || err != nil {
			t.Errorf("got (%v, %v), want (true, nil)", valid, err)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "context")); string(got) != wekafsSelinuxContext {
			t.Errorf("wrote %q, want %q with no trailing newline", got, wekafsSelinuxContext)
		}
	})
	for name, tc := range map[string]struct {
		err     error
		want    bool
		wantErr bool
	}{
		"EINVAL: type unknown to policy": {syscall.EINVAL, false, false},
		"EROFS: selinuxfs read-only":     {syscall.EROFS, false, true},
		"EACCES":                         {syscall.EACCES, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			fakeSelinuxContextWrite(t, &os.PathError{Op: "write", Path: "context", Err: tc.err})
			valid, err := selinuxContextValidAt(ctx, t.TempDir(), wekafsSelinuxContext)
			if valid != tc.want || (err != nil) != tc.wantErr {
				t.Errorf("got (%v, %v), want (%v, err=%v)", valid, err, tc.want, tc.wantErr)
			}
		})
	}
}

// SELinux being on is not enough for wekafs: the Weka policy module must define wekafs_csi_volume_t,
// otherwise a labelled mount fails. Hosts without the module must mount without a context.
func TestWekafsSelinuxDecision(t *testing.T) {
	for name, tc := range map[string]struct {
		enforce   string // "" means no selinuxfs at all
		writeErr  error
		want      bool
		wantLevel string // log level expected in output, "" for none
	}{
		"no selinuxfs":                         {"", nil, false, ""},
		"enforcing, policy loaded":             {"1", nil, true, ""},
		"permissive, policy loaded":            {"0", nil, true, ""},
		"enforcing, policy not loaded":         {"1", syscall.EINVAL, false, "warn"},
		"enforcing, selinuxfs read-only":       {"1", syscall.EROFS, true, "error"},
		"enforcing, context check not allowed": {"1", syscall.EACCES, true, "error"},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := zerolog.New(&buf).WithContext(context.Background())
			dir := filepath.Join(t.TempDir(), "selinux")
			if tc.enforce != "" {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "enforce"), []byte(tc.enforce), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var writeErr error
			if tc.writeErr != nil {
				writeErr = &os.PathError{Op: "write", Path: "context", Err: tc.writeErr}
			}
			fakeSelinuxContextWrite(t, writeErr)

			m := &wekafsMounter{selinuxfsPath: dir}
			if got := m.getSelinuxStatus(ctx); got != tc.want {
				t.Errorf("getSelinuxStatus() = %v, want %v", got, tc.want)
			}
			out := buf.String()
			if tc.wantLevel == "" && (strings.Contains(out, `"level":"warn"`) || strings.Contains(out, `"level":"error"`)) {
				t.Errorf("expected no warning or error, got %s", out)
			}
			// The result is cached, negative included: a second call must not probe or log again.
			buf.Reset()
			if got := m.getSelinuxStatus(ctx); got != tc.want || buf.Len() != 0 {
				t.Errorf("second getSelinuxStatus() = %v with log %q, want cached %v and no log", got, buf.String(), tc.want)
			}
			if tc.wantLevel != "" && !strings.Contains(out, `"level":"`+tc.wantLevel+`"`) {
				t.Errorf("expected a %s log, got %s", tc.wantLevel, out)
			}
		})
	}
}

// --selinux-support bypasses detection entirely, even with no selinuxfs and no policy.
func TestWekafsSelinuxForced(t *testing.T) {
	fakeSelinuxContextWrite(t, syscall.EINVAL)
	forced := true
	m := &wekafsMounter{selinuxSupport: &forced, selinuxfsPath: filepath.Join(t.TempDir(), "missing")}
	if !m.getSelinuxStatus(context.Background()) {
		t.Error("forced SELinux support was overridden by detection")
	}
}
