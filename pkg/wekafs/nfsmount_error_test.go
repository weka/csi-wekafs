package wekafs

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

const k8sNoExportErr = "mount failed: exit status 32\nMounting command: mount\nMounting arguments: -t nfs -o async 10.0.112.193:/default /mnt\nOutput: mount.nfs: mounting 10.0.112.193:/default failed, reason given by server: No such file or directory\n"

func TestNfsVersionLabel(t *testing.T) {
	for in, want := range map[apiclient.NfsVersionString]string{"V4": "NFSv4", "V4.1": "NFSv4", "V3": "NFSv3", "V3.0": "NFSv3", "": "NFSv4"} {
		if got := in.Label(); got != want {
			t.Errorf("Label(%q)=%q want %q", in, got, want)
		}
	}
}

func TestExtractNfsMountReason(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"server reason", k8sNoExportErr, "No such file or directory"},
		{"output line", "mount failed: exit status 32\nOutput: mount.nfs: Connection refused\n", "mount.nfs: Connection refused"},
		{"fallback", "something odd", "something odd"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractNfsMountReason(errors.New(tc.in)); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestNewNfsMountError(t *testing.T) {
	opts := []string{"async", "hard", "rdirplus", "vers=4.1"}
	details := "Details: mount -t nfs -o async,hard,rdirplus,vers=4.1 10.0.112.193:/default -> \"No such file or directory\""
	head := "NFS export missing: NFS server 10.0.112.193 does not export filesystem \"default\" to this client (10.0.108.201).\n"
	cases := map[string]struct {
		manage bool
		want   string
	}{
		"admin manages NFS": {false, head +
			"NFS permissions management is disabled (--manage-nfs-permissions=false), so the WEKA cluster admin must create an NFS client group with a rule covering this node's IP (a subnet rule covers all K8s nodes),\n" +
			"and an NFS permission for filesystem \"default\" to that group allowing NFSv4.\n" + details},
		"plugin manages NFS": {true, head +
			"The plugin manages NFS permissions (--manage-nfs-permissions=true); check the plugin logs for errors creating the NFS client group, its rule for this node's IP, or the NFS permission for filesystem \"default\".\n" + details},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := newNfsMountError("10.0.112.193", "default", "10.0.108.201", "V4.1", "10.0.112.193:/default", opts, tc.manage, errors.New(k8sNoExportErr))
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("code %v", status.Code(err))
			}
			if got := status.Convert(err).Message(); got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}

	for _, manage := range []bool{false, true} {
		err := newNfsMountError("10.0.112.193", "default", "10.0.108.201", "V3", "10.0.112.193:/default", opts, manage, errors.New("Output: mount.nfs: Connection timed out"))
		msg := status.Convert(err).Message()
		if status.Code(err) != codes.Internal || strings.Contains(msg, "export missing") ||
			!strings.HasPrefix(msg, "NFS mount of filesystem \"default\" from server 10.0.112.193 failed for client 10.0.108.201: mount.nfs: Connection timed out\nDetails:") {
			t.Errorf("unexpected (manage=%v): %s", manage, msg)
		}
	}
}

func TestGrpcStatusError(t *testing.T) {
	if grpcStatusError(errors.New("plain")) != nil {
		t.Error("plain error must not be preserved")
	}
	e := status.Error(codes.FailedPrecondition, "x")
	if grpcStatusError(e) != e {
		t.Error("status error must be preserved")
	}
}
