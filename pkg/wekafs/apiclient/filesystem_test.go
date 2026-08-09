package apiclient

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestFileSystemResizeRequest_ThickMarshalsTotalCapacityOnly(t *testing.T) {
	cap := int64(300 * 1024 * 1024 * 1024)
	r := NewFileSystemResizeRequest(uuid.New(), &cap)
	b, err := json.Marshal(r)
	assert.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, "total_capacity")
	assert.NotContains(t, s, "thin_provision_min_ssd")
	assert.NotContains(t, s, "thin_provision_max_ssd")
}

func TestFileSystemResizeRequest_ThinMarshalsThinParams(t *testing.T) {
	cap := int64(300 * 1024 * 1024 * 1024)
	min := int64(1 * 1024 * 1024 * 1024)
	max := int64(9 * 1024 * 1024 * 1024)
	r := NewFileSystemResizeRequest(uuid.New(), &cap)
	r.ThinProvisionMinSsd = &min
	r.ThinProvisionMaxSsd = &max
	b, err := json.Marshal(r)
	assert.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, "total_capacity")
	assert.Contains(t, s, "thin_provision_min_ssd")
	assert.Contains(t, s, "thin_provision_max_ssd")
}

// A filesystem still carrying NFS permissions is refused with HTTP 400 and its own exception class.
// Its caller clears the permissions and retries on seeing that error, so the error has to survive
// classification - which it did not: everything the switch did not recognise fell through to a bare
// return nil, and the caller read a refused deletion as a completed one.
//
// The symptom this test pins down was every filesystem-backed volume on the NFS transport being
// reported deleted to Kubernetes and left on the cluster.
func TestClassifyFilesystemDeleteErrorKeepsUnrecognisedFailures(t *testing.T) {
	badRequestWith := func(classes ...string) error {
		return &ApiBadRequestError{ApiResponse: &ApiResponse{ErrorCodes: classes}}
	}
	nfsBlock := badRequestWith(ExceptionClassFilesystemInUseByNfs, "OperationFailedException")
	queueFull := badRequestWith(ExceptionClassTooManyTasks, "BadStateException")
	other := errors.New("cluster unreachable")

	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{name: "success stays success", err: nil, want: nil},
		{name: "404 means already gone", err: &ApiNotFoundError{}, want: ObjectNotFoundError},
		{
			name: "400 saying the filesystem does not exist means already gone",
			err:  badRequestWith("FilesystemDoesNotExistException"),
			want: ObjectNotFoundError,
		},
		{
			// The regression: this has to reach the caller for the NFS cleanup to ever run.
			name: "400 blocked by NFS permissions is returned, not swallowed",
			err:  nfsBlock,
			want: nfsBlock,
		},
		{name: "a full task queue is returned, not swallowed", err: queueFull, want: queueFull},
		{name: "anything else is returned unchanged", err: other, want: other},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyFilesystemDeleteError(tc.err))
		})
	}

	// And the error the caller keys its retry on is still recognisable after classification.
	assert.True(t, IsFilesystemInUseByNfsError(classifyFilesystemDeleteError(nfsBlock)))
}
