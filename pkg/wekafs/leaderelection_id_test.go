package wekafs

import "testing"

// The metrics server and the CSI controller elect leaders for unrelated reasons, so they must not
// compete for one lease. When they did, the metrics server could win the controller's lease and both
// controller pods sat waiting for a leadership they would never get - no gRPC server, every sidecar
// gated by wait-for-leader, and provisioning stopped while every pod still reported healthy.
func TestLeaderElectionIDIsSeparatePerRole(t *testing.T) {
	const driver = "csi.weka.io"

	controller := leaderElectionIDForMode(driver, CsiModeController)
	all := leaderElectionIDForMode(driver, CsiModeAll)
	metricsServer := leaderElectionIDForMode(driver, CsiModeMetricsServer)

	if metricsServer == controller {
		t.Errorf("metrics server and controller share the lease %q - they would compete for it", controller)
	}

	// A driver serving the controller service must keep the same lease whichever mode it runs in,
	// or an "all" pod and a "controller" pod in one release would each elect their own leader.
	if all != controller {
		t.Errorf("CsiModeAll uses %q but CsiModeController uses %q - both serve the controller service", all, controller)
	}

	// Unchanged on purpose: renaming it would split leadership across a rolling upgrade.
	if controller != "csi.weka.io-controller-leader" {
		t.Errorf("controller lease is %q, want csi.weka.io-controller-leader - renaming splits leadership mid-upgrade", controller)
	}

	// Two drivers installed side by side must not collide either.
	if leaderElectionIDForMode("other.weka.io", CsiModeMetricsServer) == metricsServer {
		t.Error("lease name does not vary by driver name - two installs would compete")
	}
}
