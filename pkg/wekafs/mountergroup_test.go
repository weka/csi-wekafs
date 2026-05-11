package wekafs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Helper function to create driver config for tests
func createTestDriverConfig(useNfs, allowNfsFailback bool) *DriverConfig {
	mutuallyExclusive := MutuallyExclusiveMountOptsStrings{"readcache,writecache,coherent,forcedirect", "sync,async", "ro,rw"}
	return NewDriverConfig(
		"csi-volumes",
		"csi-vol-", "csi-snap-",
		"csi-seed-snap-",
		true,
		true,
		true,
		true,
		true,
		true,
		true,
		mutuallyExclusive,
		1,
		1,
		1,
		1,
		1,
		1,
		1,
		10, 5,
		true,
		allowNfsFailback,
		useNfs,
		"",
		"",
		"4.1",
		"v1",
		false,
		false,
		true,
		"",
		false,
		120*time.Second,
		60*time.Second,
		5,
		false,
		10,
		60*time.Second,
		false,
		"",
		false,
		false,
	)
}

// Helper function to create minimal driver for tests
func createTestDriver(t *testing.T, driverConfig *DriverConfig) *WekaFsDriver {
	driver, err := NewWekaFsDriver(
		"csi.weka.io",
		"test-node",
		"unix:///tmp/csi-test.sock",
		10,
		"v1.0-test",
		CsiModeAll,
		false,
		driverConfig,
	)
	assert.NoError(t, err)
	assert.NotNil(t, driver)
	return driver
}

// TestNewMounterGroup_UseNfsTrue verifies that when useNfs=true, NFS mounter is selected
func TestNewMounterGroup_UseNfsTrue(t *testing.T) {
	ctx := context.Background()
	config := createTestDriverConfig(true, false)
	driver := createTestDriver(t, config)

	mg := NewMounterGroup(ctx, driver)

	assert.NotNil(t, mg)
	assert.NotNil(t, mg.nfs)
	assert.NotNil(t, mg.wekafs)

	// NFS should be enabled
	assert.True(t, mg.nfs.isEnabled(), "NFS mounter should be enabled when useNfs=true")

	// WekaFS should be disabled
	assert.False(t, mg.wekafs.isEnabled(), "WekaFS mounter should be disabled when useNfs=true")

	// GetPreferredMounter should return NFS
	preferred := mg.GetPreferredMounter(ctx)
	assert.NotNil(t, preferred, "GetPreferredMounter should not be nil")
	assert.Equal(t, dataTransportNfs, preferred.getTransport(), "Preferred mounter should be NFS when useNfs=true")
}

// TestNewMounterGroup_UseNfsFalse verifies that when useNfs=false and allowNfsFailback=false, WekaFS is preferred
func TestNewMounterGroup_UseNfsFalse(t *testing.T) {
	ctx := context.Background()
	config := createTestDriverConfig(false, false)
	driver := createTestDriver(t, config)

	mg := NewMounterGroup(ctx, driver)

	assert.NotNil(t, mg)

	// WekaFS should be enabled
	assert.True(t, mg.wekafs.isEnabled(), "WekaFS mounter should be enabled when useNfs=false and allowNfsFailback=false")

	// NFS should not be explicitly enabled
	assert.False(t, mg.nfs.isEnabled(), "NFS mounter should not be enabled when useNfs=false and allowNfsFailback=false")

	// GetPreferredMounter should return WekaFS
	preferred := mg.GetPreferredMounter(ctx)
	assert.NotNil(t, preferred, "GetPreferredMounter should not be nil")
	assert.Equal(t, dataTransportWekafs, preferred.getTransport(), "Preferred mounter should be WekaFS when useNfs=false")
}

// TestNewMounterGroup_AllowNfsFailback verifies that when allowNfsFailback=true, NFS is enabled
func TestNewMounterGroup_AllowNfsFailback(t *testing.T) {
	ctx := context.Background()
	config := createTestDriverConfig(false, true)
	driver := createTestDriver(t, config)

	mg := NewMounterGroup(ctx, driver)

	assert.NotNil(t, mg)

	// NFS should be enabled when allowNfsFailback=true
	assert.True(t, mg.nfs.isEnabled(), "NFS mounter should be enabled when allowNfsFailback=true")

	// GetPreferredMounter should return either WekaFS (if running) or NFS (if failback)
	preferred := mg.GetPreferredMounter(ctx)
	assert.NotNil(t, preferred, "GetPreferredMounter should not be nil")
	assert.True(
		t,
		preferred.getTransport() == dataTransportWekafs || preferred.getTransport() == dataTransportNfs,
		"Preferred mounter should be either WekaFS or NFS when allowNfsFailback=true",
	)
}

// TestMounterGroup_GetMounterByTransport verifies that GetMounterByTransport returns correct mounter
func TestMounterGroup_GetMounterByTransport(t *testing.T) {
	ctx := context.Background()
	config := createTestDriverConfig(false, false)
	driver := createTestDriver(t, config)

	mg := NewMounterGroup(ctx, driver)

	// Test retrieving NFS mounter
	nfsMounter := mg.GetMounterByTransport(ctx, dataTransportNfs)
	assert.NotNil(t, nfsMounter)
	assert.Equal(t, dataTransportNfs, nfsMounter.getTransport())

	// Test retrieving WekaFS mounter
	wekafsMounter := mg.GetMounterByTransport(ctx, dataTransportWekafs)
	assert.NotNil(t, wekafsMounter)
	assert.Equal(t, dataTransportWekafs, wekafsMounter.getTransport())

	// Test invalid transport
	invalidMounter := mg.GetMounterByTransport(ctx, DataTransport("invalid"))
	assert.Nil(t, invalidMounter)
}

// TestMounterGroup_GetPreferredMounter_AllDisabled verifies behavior when all mounters are disabled
func TestMounterGroup_GetPreferredMounter_AllDisabled(t *testing.T) {
	ctx := context.Background()
	config := createTestDriverConfig(false, false)
	driver := createTestDriver(t, config)

	mg := NewMounterGroup(ctx, driver)

	// Disable all mounters
	mg.nfs.Disable()
	mg.wekafs.Disable()

	// GetPreferredMounter should return nil
	preferred := mg.GetPreferredMounter(ctx)
	assert.Nil(t, preferred, "GetPreferredMounter should return nil when all mounters are disabled")
}

// TestMounterGroup_TransportPreferenceOrder verifies that TransportPreference is correctly ordered
func TestMounterGroup_TransportPreferenceOrder(t *testing.T) {
	ctx := context.Background()
	config := createTestDriverConfig(false, false)
	driver := createTestDriver(t, config)

	mg := NewMounterGroup(ctx, driver)

	// Enable both
	mg.nfs.Enable()
	mg.wekafs.Enable()

	// According to TransportPreference = [dataTransportWekafs, dataTransportNfs]
	// GetPreferredMounter should return WekaFS first (even though NFS is enabled)
	preferred := mg.GetPreferredMounter(ctx)
	assert.NotNil(t, preferred)
	assert.Equal(t, dataTransportWekafs, preferred.getTransport(), "WekaFS should be preferred over NFS according to TransportPreference")
}

// TestMounterGroup_NfsAndWekafsMountersNotNil verifies that both mounters are created
func TestMounterGroup_NfsAndWekafsMountersNotNil(t *testing.T) {
	ctx := context.Background()
	config := createTestDriverConfig(false, false)
	driver := createTestDriver(t, config)

	mg := NewMounterGroup(ctx, driver)

	assert.NotNil(t, mg.nfs, "NFS mounter should not be nil")
	assert.NotNil(t, mg.wekafs, "WekaFS mounter should not be nil")
}

// TestMounterGroup_EnableDisable verifies that Enable and Disable work correctly
func TestMounterGroup_EnableDisable(t *testing.T) {
	ctx := context.Background()
	config := createTestDriverConfig(true, false)
	driver := createTestDriver(t, config)

	mg := NewMounterGroup(ctx, driver)

	// After creation with useNfs=true, NFS should be enabled and WekaFS disabled
	assert.True(t, mg.nfs.isEnabled())
	assert.False(t, mg.wekafs.isEnabled())

	// Enable WekaFS
	mg.wekafs.Enable()
	assert.True(t, mg.wekafs.isEnabled())

	// Disable NFS
	mg.nfs.Disable()
	assert.False(t, mg.nfs.isEnabled())

	// GetPreferredMounter should now return WekaFS (first in TransportPreference)
	preferred := mg.GetPreferredMounter(ctx)
	assert.NotNil(t, preferred)
	assert.Equal(t, dataTransportWekafs, preferred.getTransport())
}

// TestMounterGroup_InitialNfsMounterDisabledByDefault verifies that NFS is disabled by default
func TestMounterGroup_InitialNfsMounterDisabledByDefault(t *testing.T) {
	ctx := context.Background()
	config := createTestDriverConfig(false, false)
	driver := createTestDriver(t, config)

	mg := NewMounterGroup(ctx, driver)

	// NFS should be disabled by default
	assert.False(t, mg.nfs.isEnabled(), "NFS mounter should be disabled by default")
}

// TestMounterGroup_GetPreferredMounterNilCheck verifies no panic on nil check
func TestMounterGroup_GetPreferredMounterNilCheck(t *testing.T) {
	ctx := context.Background()
	config := createTestDriverConfig(true, false)
	driver := createTestDriver(t, config)

	mg := NewMounterGroup(ctx, driver)

	// This should not panic even with disabled mounters
	preferred := mg.GetPreferredMounter(ctx)
	assert.NotNil(t, preferred, "Should have NFS mounter when useNfs=true")
}
