package wekafs

import (
	"context"
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

	if err := applyNodeLabels(context.Background(), client, client, "node-1", desired); err != nil {
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

	if err := applyNodeLabels(context.Background(), client, client, "node-1", desired); err != nil {
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

	if err := applyNodeLabels(context.Background(), client, client, "node-1", desired); err != nil {
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
	if err := applyNodeLabels(context.Background(), client, client, "node-1", desired); err != nil {
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

	d.SetNodeLabels(context.Background())
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

	if err := removeNodeLabels(context.Background(), client, client, "node-1", managed); err != nil {
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

// The startup cleanup runs once the manager exists but before its informer cache is started, so it
// reads through GetAPIReader and writes through GetClient. This pins that split: the node exists
// only in the reader, and an implementation that read through the writer instead would not find it.
// Collapsing the two back onto the cached client is what made the startup cleanup a silent no-op.
func TestRemoveNodeLabels_ReadsThroughTheReaderNotTheWriter(t *testing.T) {
	driverName := "wekafs.csi.k8s.io"
	managed := managedNodeLabelKeys(driverName)

	labels := map[string]string{"unrelated": "keep-me"}
	for _, key := range managed {
		labels[key] = "some-value"
	}

	reader := fakeClient.NewClientBuilder().
		WithObjects(&v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1", Labels: labels}}).Build()
	// A writer that knows nothing about the node, standing in for a client whose cache has not
	// synced. Only the update it receives matters.
	writer := &recordingNodeWriter{}

	if err := removeNodeLabels(context.Background(), reader, writer, "node-1", managed); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if writer.updated == nil {
		t.Fatal("no update was issued, so nothing would have been cleaned up")
	}
	for _, key := range managed {
		if _, ok := writer.updated.Labels[key]; ok {
			t.Errorf("expected managed label %q to be removed, got %v", key, writer.updated.Labels)
		}
	}
	if writer.updated.Labels["unrelated"] != "keep-me" {
		t.Errorf("expected unrelated label to survive, got %v", writer.updated.Labels)
	}
}

// recordingNodeWriter captures the Update it is handed and implements nothing else.
type recordingNodeWriter struct {
	runtimeclient.Writer
	updated *v1.Node
}

func (w *recordingNodeWriter) Update(_ context.Context, obj runtimeclient.Object, _ ...runtimeclient.UpdateOption) error {
	node, ok := obj.(*v1.Node)
	if !ok {
		return nil
	}
	w.updated = node.DeepCopy()
	return nil
}

// The first Probe can land before the manager's cache has synced, and a cached read fails outright
// then rather than waiting. cacheThenLive covers that window: the node is only in the live reader
// here, standing in for an unsynced cache, and the labels must still be applied.
func TestApplyNodeLabels_FallsBackToLiveReadWhenCacheIsNotReady(t *testing.T) {
	live := fakeClient.NewClientBuilder().
		WithObjects(&v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}).Build()
	// An empty cache, which is what a cached Get hits before the informers have synced.
	cold := fakeClient.NewClientBuilder().Build()
	writer := &recordingNodeWriter{}

	desired := map[string]string{"topology.example/node": "node-1"}
	reader := cacheThenLive{cached: cold, live: live}
	if err := applyNodeLabels(context.Background(), reader, writer, "node-1", desired); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if writer.updated == nil {
		t.Fatal("no update was issued, so the labels would stay missing until a later Probe")
	}
	if writer.updated.Labels["topology.example/node"] != "node-1" {
		t.Errorf("expected the label to be applied, got %v", writer.updated.Labels)
	}
}

// Once the cache is warm it must be the one serving reads: a direct API call per Probe, on every
// node every ten seconds, is what the cached client exists to avoid.
func TestApplyNodeLabels_PrefersTheCacheWhenItIsWarm(t *testing.T) {
	warm := fakeClient.NewClientBuilder().
		WithObjects(&v1.Node{ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: map[string]string{"from": "cache"},
		}}).Build()
	live := fakeClient.NewClientBuilder().
		WithObjects(&v1.Node{ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: map[string]string{"from": "live"},
		}}).Build()
	writer := &recordingNodeWriter{}

	reader := cacheThenLive{cached: warm, live: live}
	if err := applyNodeLabels(context.Background(), reader, writer,
		"node-1", map[string]string{"topology.example/node": "node-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if writer.updated == nil {
		t.Fatal("no update was issued")
	}
	if got := writer.updated.Labels["from"]; got != "cache" {
		t.Errorf("read came from %q, want the cache", got)
	}
}
