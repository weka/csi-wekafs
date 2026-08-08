package wekafs

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Every other metric in this package is a CounterVec or GaugeVec, which export nothing until a
// label set is first observed. plugin_info exists so that a process is discoverable before it has
// served anything - dashboard variables are built on it, and an empty variable silently queries
// nothing, which renders as a healthy zero rather than as no data.
func TestPluginInfoIsExportedBeforeAnyRequest(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	if err := reg.Register(PluginInfoCollector()); err != nil {
		t.Fatalf("expected plugin_info to be registerable: %v", err)
	}

	// Nothing observed yet: this is the state of a freshly started pod.
	if got := testutil.CollectAndCount(PluginInfoCollector()); got != 0 {
		t.Fatalf("precondition: expected no series before SetPluginInfo, got %d", got)
	}

	SetPluginInfo("csi.weka.io", CsiModeController, "v2.10.0")

	if got := testutil.CollectAndCount(PluginInfoCollector()); got != 1 {
		t.Errorf("expected exactly one plugin_info series after startup, got %d", got)
	}
	// The mode label is the only thing in the metric surface that tells a controller process from a
	// node one, so a query can split the two roles by it.
	if got := testutil.ToFloat64(pluginInfo.WithLabelValues("csi.weka.io", string(CsiModeController), "v2.10.0")); got != 1 {
		t.Errorf("expected plugin_info to be 1 for this process, got %v", got)
	}
}
