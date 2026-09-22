package wekafs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The Weka cluster says "object not found" for two conditions that mean opposite things: a volume
// whose quota was never created, which is intact and merely unenforced, and a volume whose
// directory - or, for a snapshot-backed volume, the access point it is reached through - is gone.
// Now that the extended attribute is no longer read, whichever of the two comes back is what an
// operator sees when a capacity read or an expand fails, so they must not be collapsed into one.
func TestMissingQuotaAndMissingPathAreToldApart(t *testing.T) {
	if explicitEndpoint {
		t.Skip("runs against the in-memory fake cluster only")
	}
	driver := GetDriverForTest(t)
	apiClient := GetApiClientForTest(t)
	ctx := context.Background()

	// The fake resolves only the filesystem root, and holds no quota for it: the path is there,
	// the quota is not.
	intact, err := NewVolumeFromId(ctx, "weka/v2/default", apiClient, driver.cs)
	if err != nil {
		t.Fatalf("building the filesystem-backed volume: %v", err)
	}
	_, err = intact.getQuota(ctx)
	if !errors.Is(err, ErrVolumeHasNoQuota) {
		t.Fatalf("volume with no quota reported %v, want ErrVolumeHasNoQuota", err)
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("missing quota reported as %v, want FailedPrecondition", st.Code())
	}
	msg := strings.ToLower(st.Message())
	if !strings.Contains(msg, "no quota") || strings.Contains(msg, "object not found") {
		t.Errorf("missing quota still reads as a missing object: %q", st.Message())
	}
	if !strings.Contains(msg, "usable") && !strings.Contains(msg, "intact") {
		t.Errorf("message does not say the volume is still usable: %q", st.Message())
	}

	// Same filesystem, a directory the fake will not resolve. Reporting this as "no quota, the
	// volume is intact" would send an operator to create a quota for data that is gone.
	gone, err := NewVolumeFromId(ctx, "weka/v2/default/csi-volumes/vol-that-was-deleted", apiClient, driver.cs)
	if err != nil {
		t.Fatalf("building the directory-backed volume: %v", err)
	}
	_, err = gone.getQuota(ctx)
	if errors.Is(err, ErrVolumeHasNoQuota) {
		t.Fatalf("a volume whose path is gone was reported as merely missing its quota: %v", err)
	}
	if !errors.Is(err, ErrVolumePathNotFound) {
		t.Fatalf("volume with no path reported %v, want ErrVolumePathNotFound", err)
	}
	if st, _ := status.FromError(err); st.Code() != codes.NotFound {
		t.Errorf("missing path reported as %v, want NotFound", st.Code())
	}
}
