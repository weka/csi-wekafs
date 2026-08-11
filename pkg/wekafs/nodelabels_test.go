package wekafs

import (
	"context"
	"fmt"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func nodeKey(name string) runtimeclient.ObjectKey {
	return runtimeclient.ObjectKey{Name: name}
}

func TestApplyNodeLabels_AppliesToNodeWithNoLabels(t *testing.T) {
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	client := fakeClient.NewClientBuilder().WithObjects(node).Build()
	desired := map[string]string{"topology.example/node": "node-1", "topology.example/accessible": "true"}

	if err := applyNodeLabels(context.Background(), client, "node-1", desired); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := &v1.Node{}
	if err := client.Get(context.Background(), nodeKey("node-1"), got); err != nil {
		t.Fatalf("unexpected error re-reading node: %v", err)
	}
	if got.Labels["topology.example/node"] != "node-1" || got.Labels["topology.example/accessible"] != "true" {
		t.Errorf("expected both labels to be applied, got %v", got.Labels)
	}
}

func TestApplyNodeLabels_AlreadyCorrectMakesNoUpdateCall(t *testing.T) {
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "node-1",
		Labels: map[string]string{"topology.example/node": "node-1"},
	}}
	client := fakeClient.NewClientBuilder().WithObjects(node).Build()
	desired := map[string]string{"topology.example/node": "node-1"}

	before := &v1.Node{}
	if err := client.Get(context.Background(), nodeKey("node-1"), before); err != nil {
		t.Fatalf("unexpected error reading node: %v", err)
	}
	rvBefore := before.ResourceVersion

	if err := applyNodeLabels(context.Background(), client, "node-1", desired); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after := &v1.Node{}
	if err := client.Get(context.Background(), nodeKey("node-1"), after); err != nil {
		t.Fatalf("unexpected error re-reading node: %v", err)
	}
	if after.ResourceVersion != rvBefore {
		t.Errorf("expected no Update call when labels already match (resourceVersion unchanged), before=%s after=%s", rvBefore, after.ResourceVersion)
	}
}

func TestApplyNodeLabels_ExternallyChangedLabelIsCorrected(t *testing.T) {
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "node-1",
		Labels: map[string]string{"topology.example/node": "wrong-value"},
	}}
	client := fakeClient.NewClientBuilder().WithObjects(node).Build()
	desired := map[string]string{"topology.example/node": "node-1"}

	if err := applyNodeLabels(context.Background(), client, "node-1", desired); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := &v1.Node{}
	if err := client.Get(context.Background(), nodeKey("node-1"), got); err != nil {
		t.Fatalf("unexpected error re-reading node: %v", err)
	}
	if got.Labels["topology.example/node"] != "node-1" {
		t.Errorf("expected externally-changed label to be corrected, got %v", got.Labels)
	}
}

func TestApplyNodeLabels_TransportLabelReflectsChangedDesiredValue(t *testing.T) {
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "node-1",
		Labels: map[string]string{"topology.example/transport": "wekafs"},
	}}
	client := fakeClient.NewClientBuilder().WithObjects(node).Build()

	// Transport flips - e.g. NFS failback.
	desired := map[string]string{"topology.example/transport": "nfs"}
	if err := applyNodeLabels(context.Background(), client, "node-1", desired); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := &v1.Node{}
	if err := client.Get(context.Background(), nodeKey("node-1"), got); err != nil {
		t.Fatalf("unexpected error re-reading node: %v", err)
	}
	if got.Labels["topology.example/transport"] != "nfs" {
		t.Errorf("expected updated transport label on node, got %v", got.Labels)
	}
}

func TestSetNodeLabels_NilManagerDoesNotPanic(t *testing.T) {
	d := &WekaFsDriver{
		name:    "wekafs.csi.k8s.io",
		nodeID:  "node-1",
		csiMode: CsiModeNode,
		config:  &DriverConfig{},
		// manager is left nil: this is the crash case being tested.
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SetNodeLabels panicked with nil manager: %v", r)
		}
	}()

	d.SetNodeLabels(context.Background(), true)
}

func TestCleanupNodeLabels_NilManagerDoesNotPanic(t *testing.T) {
	d := &WekaFsDriver{
		name:    "wekafs.csi.k8s.io",
		nodeID:  "node-1",
		csiMode: CsiModeNode,
		config:  &DriverConfig{},
		// manager is left nil: this is the crash case being tested.
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CleanupNodeLabels panicked with nil manager: %v", r)
		}
	}()

	d.CleanupNodeLabels(context.Background())
}

func TestRemoveNodeLabels_ClearsManagedLabelsOnly(t *testing.T) {
	driverName := "wekafs.csi.k8s.io"
	managed := managedNodeLabelKeys(driverName)

	labels := map[string]string{"unrelated": "keep-me"}
	for _, key := range managed {
		labels[key] = "some-value"
	}

	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1", Labels: labels}}
	client := fakeClient.NewClientBuilder().WithObjects(node).Build()

	if err := removeNodeLabels(context.Background(), client, "node-1", managed); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := &v1.Node{}
	if err := client.Get(context.Background(), nodeKey("node-1"), got); err != nil {
		t.Fatalf("unexpected error re-reading node: %v", err)
	}
	for _, key := range managed {
		if _, ok := got.Labels[key]; ok {
			t.Errorf("expected managed label %q to be removed, got %v", key, got.Labels)
		}
	}
	if got.Labels["unrelated"] != "keep-me" {
		t.Errorf("expected unrelated label to survive, got %v", got.Labels)
	}
}

// TestSetNodeLabels_UsesCallerSuppliedTransport covers the reason this takes a parameter at all.
// SetNodeLabels used to call isWekaRunning itself, which re-read and re-parsed the driver's frontend
// list on every probe even though Probe had just done exactly that - visible in the logs as "All
// frontends connected" printed twice, a few milliseconds apart, every ten seconds on every node.
//
// It now labels from what the caller passes. Asserting both directions is what makes the parameter
// load-bearing: if the value were ignored and the answer re-derived, one of these two cases would
// disagree with the host it runs on rather than with the argument.
func TestSetNodeLabels_UsesCallerSuppliedTransport(t *testing.T) {
	for _, tt := range []struct {
		name             string
		allowNfsFailback bool
		wekafsAvailable  bool
		want             string
	}{
		{"weka up, failback allowed", true, true, "wekafs"},
		{"weka down, failback allowed", true, false, "nfs"},
		{"weka down, no failback - stays wekafs", false, false, "wekafs"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
			fc := fakeClient.NewClientBuilder().WithObjects(node).Build()
			d := &WekaFsDriver{
				name:    "topology.example",
				nodeID:  "node-1",
				csiMode: CsiModeNode,
				config:  &DriverConfig{allowNfsFailback: tt.allowNfsFailback},
				manager: &fakeHealthReconcilerManager{client: fc},
			}

			d.SetNodeLabels(context.Background(), tt.wekafsAvailable)

			got := &v1.Node{}
			if err := fc.Get(context.Background(), runtimeclient.ObjectKey{Name: "node-1"}, got); err != nil {
				t.Fatalf("failed to read the node back: %v", err)
			}
			label := fmt.Sprintf(TopologyLabelTransportPattern, d.name)
			if got.Labels[label] != tt.want {
				t.Fatalf("transport label = %q, want %q (labels: %v)", got.Labels[label], tt.want, got.Labels)
			}
		})
	}
}
