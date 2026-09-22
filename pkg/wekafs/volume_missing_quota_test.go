package wekafs

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// A volume with no quota is the case the 3.0 upgrade notes call the most likely one to meet, and
// the cluster describes it with the same words it uses for a filesystem that is gone. Now that the
// extended attribute is no longer read, that answer is what an expand or a capacity read returns,
// so it has to say which of the two happened.
func TestMissingQuotaIsNotReportedAsAMissingVolume(t *testing.T) {
	err := capacityFromQuotaError(apiclient.ObjectNotFoundError)

	if !errors.Is(err, ErrVolumeHasNoQuota) {
		t.Fatalf("a missing quota produced %v, want ErrVolumeHasNoQuota", err)
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		t.Errorf("missing quota reported as %v, want FailedPrecondition", st.Code())
	}
	msg := strings.ToLower(st.Message())
	if !strings.Contains(msg, "no quota") {
		t.Errorf("message does not say the quota is missing: %q", st.Message())
	}
	if strings.Contains(msg, "object not found") {
		t.Errorf("message still reads as a missing object: %q", st.Message())
	}
	// The distinction only helps if it survives: an operator who is told the capacity cannot be
	// read needs to know the volume itself is not the thing that is gone.
	if !strings.Contains(msg, "usable") && !strings.Contains(msg, "intact") {
		t.Errorf("message does not say the volume is still usable: %q", st.Message())
	}
}

// Everything that is not a missing quota has to pass through untouched, or a real failure gets
// dressed up as a benign one and the repair advice sends the operator the wrong way.
func TestOtherQuotaErrorsAreNotRewritten(t *testing.T) {
	other := errors.New("connection refused")
	if got := capacityFromQuotaError(other); got != other {
		t.Errorf("unrelated error was rewritten to %v", got)
	}
	if got := capacityFromQuotaError(nil); got != nil {
		t.Errorf("nil error became %v", got)
	}
}
