package apiclient

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient/apiclienttest"
)

// newDataServicesTestClient builds a client against a fake cluster, optionally one that reports a
// DATASERV process.
func newDataServicesTestClient(t *testing.T, version string, withDataServices bool) *ApiClient {
	t.Helper()
	opts := []apiclienttest.Option{apiclienttest.WithClusterVersion(version)}
	if withDataServices {
		opts = append(opts, apiclienttest.WithDataServicesProcess())
	}
	server := apiclienttest.New(t, opts...)
	c, err := NewApiClient(context.Background(), Credentials{
		Username:     "admin",
		Password:     "admin",
		Organization: "Root",
		HttpScheme:   "http",
		Endpoints:    []string{server.Addr()},
	}, ApiClientOptions{AllowInsecureHttps: true, Hostname: "dataservices-test"})
	require.NoError(t, err)
	require.NoError(t, c.Init(context.Background()))
	return c
}

// TestHasDataServicesProcess drives the real HTTP path: the client fetches the process listing from
// the fake cluster and looks for the DATASERV role.
func TestHasDataServicesProcess(t *testing.T) {
	t.Run("cluster without a data services container", func(t *testing.T) {
		c := newDataServicesTestClient(t, "5.1.24", false)
		got, err := c.HasDataServicesProcess(context.Background())
		assert.NoError(t, err)
		assert.False(t, got)
	})

	t.Run("cluster with a data services container", func(t *testing.T) {
		c := newDataServicesTestClient(t, "5.1.24", true)
		got, err := c.HasDataServicesProcess(context.Background())
		assert.NoError(t, err)
		assert.True(t, got)
	})
}

// TestSupportsQuotaOnNonEmptyDirectory covers the three outcomes that need different remedies.
func TestSupportsQuotaOnNonEmptyDirectory(t *testing.T) {
	testCases := []struct {
		name             string
		version          string
		withDataServices bool
		expect           QuotaOnNonEmptyDirectorySupport
	}{
		{
			// Too old for the role to exist. Upgrading, or setting the quota through the WEKA CLI,
			// are the only ways forward - so this must not be reported as "deploy a container".
			name:             "cluster predates the data services role",
			version:          "4.2",
			withDataServices: false,
			expect:           QuotaOnNonEmptyDirectoryVersionTooOld,
		},
		{
			// The common case: new enough, but the container is opt-in and was never deployed.
			name:             "new enough but no container deployed",
			version:          "4.3",
			withDataServices: false,
			expect:           QuotaOnNonEmptyDirectoryNoContainer,
		},
		{
			name:             "supported",
			version:          "5.1.24",
			withDataServices: true,
			expect:           QuotaOnNonEmptyDirectorySupported,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := newDataServicesTestClient(t, tc.version, tc.withDataServices)
			got, err := c.SupportsQuotaOnNonEmptyDirectory(context.Background())
			assert.NoError(t, err)
			assert.Equal(t, tc.expect, got)
		})
	}
}

// TestDataServicesRoleIsNotAContainerMode pins the fact that cost real debugging time: the WEKA API
// reports a data services container as an ordinary "backend", and only the process role
// distinguishes it. A future refactor that goes looking for a "data-services" container mode would
// silently find nothing on every real cluster.
func TestDataServicesRoleIsNotAContainerMode(t *testing.T) {
	n := WekaNode{Roles: []string{NodeRoleDataServices}, Mode: NodeModeBackend, Status: "UP"}
	assert.True(t, n.isDataServices())
	assert.Equal(t, "backend", n.Mode, "a data services process still reports container mode backend")
}
