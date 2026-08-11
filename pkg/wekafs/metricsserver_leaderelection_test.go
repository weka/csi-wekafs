package wekafs

import "testing"

// The chart value promised that turning leader election off makes every replica collect. It did not:
// only the readiness check consulted it, while the manager elected regardless, so the collection
// runnable stayed gated on leadership. Standbys then reported Ready and collected nothing - the one
// outcome worse than an honest standby, because nothing in pod status said so.
//
// This pins the value reaching the manager, which is the decision that actually gates collection.
func TestMetricsServerElectsLeaderFollowsTheConfiguredValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *DriverConfig
		want bool
	}{
		{
			name: "election on - one replica collects, the rest stand by",
			cfg:  &DriverConfig{enableMetricsServerLeaderElection: true},
			want: true,
		},
		{
			// The case that was broken: with no lease, controller-runtime treats every replica as
			// elected and starts the collection runnable on all of them.
			name: "election off - every replica collects",
			cfg:  &DriverConfig{enableMetricsServerLeaderElection: false},
			want: false,
		},
		{
			name: "no configuration - elect, matching the chart default",
			cfg:  nil,
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := metricsServerElectsLeader(tc.cfg); got != tc.want {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

// The flag is plumbed from a CLI flag through DriverConfig, so a rename or a dropped assignment on
// the way would silently restore the old always-elect behaviour.
func TestMetricsServerLeaderElectionSurvivesConfigConstruction(t *testing.T) {
	for _, want := range []bool{true, false} {
		cfg := NewDriverConfig(DriverConfigOptions{EnableMetricsServerLeaderElection: want})
		if got := metricsServerElectsLeader(cfg); got != want {
			t.Errorf("EnableMetricsServerLeaderElection=%v reached the manager as %v", want, got)
		}
	}
}

// The health-probe server is what answers /healthz, and the metrics server deployment has a liveness
// probe pointed at it. Binding it used to be gated on leader election, which was indistinguishable
// from the mode until the metrics server could run without a lease - so turning election off would
// have left that pod with a liveness probe and nothing listening, i.e. a crash loop.
//
// Node pods are the reason the gate exists at all: they share the host network namespace with the
// controller, so a port bound there blocks the controller manager.
func TestHealthProbesFollowTheModeNotLeaderElection(t *testing.T) {
	for _, tc := range []struct {
		mode CsiPluginMode
		want bool
		why  string
	}{
		{mode: CsiModeController, want: true, why: "controller pods have liveness probes"},
		{mode: CsiModeAll, want: true, why: "combined pods serve the controller probes"},
		{mode: CsiModeMetricsServer, want: true, why: "has a liveness probe whether or not it elects"},
		{mode: CsiModeNode, want: false, why: "would take a port the controller manager needs"},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			if got := tc.mode.servesHealthProbes(); got != tc.want {
				t.Errorf("%s: expected %v, got %v (%s)", tc.mode, tc.want, got, tc.why)
			}
		})
	}
}
