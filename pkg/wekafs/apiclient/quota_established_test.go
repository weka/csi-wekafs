package apiclient

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient/apiclienttest"
)

// newQuotaTestClient returns a client against a fake cluster, plus the server so a test can drive
// quota status transitions.
func newQuotaTestClient(t *testing.T) (*ApiClient, *apiclienttest.Server) {
	t.Helper()
	server := apiclienttest.New(t)
	c, err := NewApiClient(context.Background(), Credentials{
		Username:     "admin",
		Password:     "admin",
		Organization: "Root",
		HttpScheme:   "http",
		Endpoints:    []string{server.Addr()},
	}, ApiClientOptions{AllowInsecureHttps: true, Hostname: "quota-test"})
	require.NoError(t, err)
	require.NoError(t, c.Init(context.Background()))
	return c, server
}

// createQuota puts a quota through the real code path and returns the identifiers needed to drive
// its status afterwards.
func createQuota(t *testing.T, c *ApiClient, wait bool) (fsUid uuid.UUID, inodeId uint64, err error) {
	t.Helper()
	fsUid = uuid.New()
	inodeId = 4242
	fs := FileSystem{Uid: fsUid}
	qr := NewQuotaCreateRequest(fs, inodeId, QuotaTypeHard, 1<<30)
	q := &Quota{}
	err = c.CreateQuota(context.Background(), qr, q, wait)
	return fsUid, inodeId, err
}

func TestIsQuotaEstablished(t *testing.T) {
	testCases := []struct {
		name      string
		status    string
		expect    bool
		expectErr bool
	}{
		{name: "active quota is established", status: QuotaStatusActive, expect: true},
		{
			// The case this whole change exists for: a quota over a directory that already holds
			// data sits in ADDING while QUOTA_COLORING walks the tree. It is already applying, so
			// the caller must not be made to wait for ACTIVE.
			name:   "adding quota is established",
			status: QuotaStatusPending,
			expect: true,
		},
		{
			// A permanent failure must surface as an error, not as a timeout.
			name:      "error quota fails fast",
			status:    QuotaStatusError,
			expect:    false,
			expectErr: true,
		},
		{name: "deleting quota is not established", status: QuotaStatusDeleting, expect: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c, server := newQuotaTestClient(t)
			fsUid, inodeId, err := createQuota(t, c, false)
			require.NoError(t, err)
			server.SetQuotaStatus(fsUid.String(), inodeId, tc.status)

			got, err := c.IsQuotaEstablished(context.Background(), &Quota{FilesystemUid: fsUid, InodeId: inodeId})
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, got)
		})
	}
}

// TestWaitForQuotaEstablishedReturnsWhileAdding is the regression guard for the behaviour this
// change exists to provide: the wait CreateQuota performs must be satisfied by ADDING, rather than
// blocking until the colouring walk finishes and the quota reaches ACTIVE.
func TestWaitForQuotaEstablishedReturnsWhileAdding(t *testing.T) {
	c, server := newQuotaTestClient(t)

	fsUid, inodeId, err := createQuota(t, c, false)
	require.NoError(t, err)
	server.SetQuotaStatus(fsUid.String(), inodeId, QuotaStatusPending)

	start := time.Now()
	err = c.WaitForQuotaEstablished(context.Background(), &Quota{FilesystemUid: fsUid, InodeId: inodeId})
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.Less(t, elapsed, 5*time.Second,
		"a quota in ADDING must satisfy the wait immediately, not block for the colouring walk")
}

// TestWaitForQuotaEstablishedHonoursContext proves the wait aborts through the context rather than
// running to its own timeout - the reason wait.Poll was replaced with PollUntilContextTimeout.
func TestWaitForQuotaEstablishedHonoursContext(t *testing.T) {
	c, server := newQuotaTestClient(t)
	fsUid, inodeId, err := createQuota(t, c, false)
	require.NoError(t, err)
	// DELETING never satisfies the condition, so the wait can only end via the context.
	server.SetQuotaStatus(fsUid.String(), inodeId, QuotaStatusDeleting)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err = c.WaitForQuotaEstablished(ctx, &Quota{FilesystemUid: fsUid, InodeId: inodeId})
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.Less(t, elapsed, quotaEstablishTimeout,
		"the wait must end with the request context, not run on to its own timeout")
}
