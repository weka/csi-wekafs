package wekafs

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// TestGetPodMountOptionsOverride_MissingPod tests behavior when pod doesn't exist
func TestGetPodMountOptionsOverride_MissingPod(t *testing.T) {
	client := fakeClient.NewClientBuilder().Build()
	ctx := context.Background()

	override := getPodMountOptionsOverride(ctx, client, "default", "nonexistent-pod", "my-pvc")

	if override != "" {
		t.Errorf("Expected empty override for non-existent pod, got '%s'", override)
	}
}

// TestGetPodMountOptionsOverride_NoAnnotation tests behavior when pod has no annotation
func TestGetPodMountOptionsOverride_NoAnnotation(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{},
		},
	}

	client := fakeClient.NewClientBuilder().WithObjects(pod).Build()
	ctx := context.Background()

	override := getPodMountOptionsOverride(ctx, client, "default", "my-pod", "my-pvc")

	if override != "" {
		t.Errorf("Expected empty override when pod has no annotation, got '%s'", override)
	}
}

// TestGetPodMountOptionsOverride_NoMatchingPattern tests when pod annotation doesn't match PVC
func TestGetPodMountOptionsOverride_NoMatchingPattern(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
			Annotations: map[string]string{
				PodMountOptionOverrideAnnotation: "other-pvc: -forcedirect, +readcache",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{},
		},
	}

	client := fakeClient.NewClientBuilder().WithObjects(pod).Build()
	ctx := context.Background()

	override := getPodMountOptionsOverride(ctx, client, "default", "my-pod", "my-pvc")

	if override != "" {
		t.Errorf("Expected empty override when pattern doesn't match, got '%s'", override)
	}
}

// TestGetPodMountOptionsOverride_MatchingPattern tests successful pattern matching
func TestGetPodMountOptionsOverride_MatchingPattern(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
			Annotations: map[string]string{
				PodMountOptionOverrideAnnotation: "my-pvc: -forcedirect, +readcache",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{},
		},
	}

	client := fakeClient.NewClientBuilder().WithObjects(pod).Build()
	ctx := context.Background()

	override := getPodMountOptionsOverride(ctx, client, "default", "my-pod", "my-pvc")

	if override != "-forcedirect, +readcache" {
		t.Errorf("Expected '-forcedirect, +readcache', got '%s'", override)
	}
}

// TestGetPodMountOptionsOverride_RegexPattern tests regex pattern matching in pod annotation
func TestGetPodMountOptionsOverride_RegexPattern(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
			Annotations: map[string]string{
				PodMountOptionOverrideAnnotation: "cache-.*: +readcache\ndb-vol-[0-9]+: +forcedirect",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{},
		},
	}

	client := fakeClient.NewClientBuilder().WithObjects(pod).Build()
	ctx := context.Background()

	// Test first pattern match
	override := getPodMountOptionsOverride(ctx, client, "default", "my-pod", "cache-data")
	if override != "+readcache" {
		t.Errorf("Expected '+readcache' for cache-data, got '%s'", override)
	}

	// Test second pattern match
	override = getPodMountOptionsOverride(ctx, client, "default", "my-pod", "db-vol-123")
	if override != "+forcedirect" {
		t.Errorf("Expected '+forcedirect' for db-vol-123, got '%s'", override)
	}

	// Test no match
	override = getPodMountOptionsOverride(ctx, client, "default", "my-pod", "other-vol")
	if override != "" {
		t.Errorf("Expected empty override for other-vol, got '%s'", override)
	}
}

// TestGetPodMountOptionsOverride_FirstMatchWins tests that first matching pattern takes precedence
func TestGetPodMountOptionsOverride_FirstMatchWins(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
			Annotations: map[string]string{
				PodMountOptionOverrideAnnotation: "my-.*: +readcache\nmy-pvc: +writecache",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{},
		},
	}

	client := fakeClient.NewClientBuilder().WithObjects(pod).Build()
	ctx := context.Background()

	// Both patterns would match "my-pvc", but first one should be used
	override := getPodMountOptionsOverride(ctx, client, "default", "my-pod", "my-pvc")
	if override != "+readcache" {
		t.Errorf("Expected '+readcache' (first pattern), got '%s'", override)
	}
}

// TestGetPvcMountOptionsOverride_MissingPvc tests behavior when PVC doesn't exist
func TestGetPvcMountOptionsOverride_MissingPvc(t *testing.T) {
	client := fakeClient.NewClientBuilder().Build()
	ctx := context.Background()

	override := getPvcMountOptionsOverride(ctx, client, "default", "nonexistent-pvc")

	if override != "" {
		t.Errorf("Expected empty override for non-existent PVC, got '%s'", override)
	}
}

// TestGetPvcMountOptionsOverride_NoAnnotation tests behavior when PVC has no annotation
func TestGetPvcMountOptionsOverride_NoAnnotation(t *testing.T) {
	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pvc",
			Namespace: "default",
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
			Resources: v1.VolumeResourceRequirements{
				Requests: v1.ResourceList{},
			},
		},
	}

	client := fakeClient.NewClientBuilder().WithObjects(pvc).Build()
	ctx := context.Background()

	override := getPvcMountOptionsOverride(ctx, client, "default", "my-pvc")

	if override != "" {
		t.Errorf("Expected empty override when PVC has no annotation, got '%s'", override)
	}
}

// TestGetPvcMountOptionsOverride_WithAnnotation tests successful PVC annotation retrieval
func TestGetPvcMountOptionsOverride_WithAnnotation(t *testing.T) {
	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pvc",
			Namespace: "default",
			Annotations: map[string]string{
				PvcMountOptionOverrideAnnotation: "-forcedirect, +readcache, +noatime",
			},
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
			Resources: v1.VolumeResourceRequirements{
				Requests: v1.ResourceList{},
			},
		},
	}

	client := fakeClient.NewClientBuilder().WithObjects(pvc).Build()
	ctx := context.Background()

	override := getPvcMountOptionsOverride(ctx, client, "default", "my-pvc")

	if override != "-forcedirect, +readcache, +noatime" {
		t.Errorf("Expected '-forcedirect, +readcache, +noatime', got '%s'", override)
	}
}

// TestGetPvcMountOptionsOverride_DifferentNamespace tests namespace isolation
func TestGetPvcMountOptionsOverride_DifferentNamespace(t *testing.T) {
	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pvc",
			Namespace: "namespace-a",
			Annotations: map[string]string{
				PvcMountOptionOverrideAnnotation: "+readcache",
			},
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
			Resources: v1.VolumeResourceRequirements{
				Requests: v1.ResourceList{},
			},
		},
	}

	client := fakeClient.NewClientBuilder().WithObjects(pvc).Build()
	ctx := context.Background()

	// Should find it in correct namespace
	override := getPvcMountOptionsOverride(ctx, client, "namespace-a", "my-pvc")
	if override != "+readcache" {
		t.Errorf("Expected '+readcache' in namespace-a, got '%s'", override)
	}

	// Should not find it in different namespace
	override = getPvcMountOptionsOverride(ctx, client, "namespace-b", "my-pvc")
	if override != "" {
		t.Errorf("Expected empty override in namespace-b, got '%s'", override)
	}
}

// TestIntegration_PodAndPvcAnnotationsCombined tests pod and PVC annotations together
func TestIntegration_PodAndPvcAnnotationsCombined(t *testing.T) {
	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: "default",
			Annotations: map[string]string{
				PvcMountOptionOverrideAnnotation: "-forcedirect, +readcache",
			},
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
			Resources: v1.VolumeResourceRequirements{
				Requests: v1.ResourceList{},
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Annotations: map[string]string{
				PodMountOptionOverrideAnnotation: "test-pvc: -readcache, +writecache",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{},
		},
	}

	client := fakeClient.NewClientBuilder().WithObjects(pvc, pod).Build()
	ctx := context.Background()

	// Get PVC override
	pvcOverride := getPvcMountOptionsOverride(ctx, client, "default", "test-pvc")
	if pvcOverride != "-forcedirect, +readcache" {
		t.Errorf("Expected PVC override '-forcedirect, +readcache', got '%s'", pvcOverride)
	}

	// Get pod override
	podOverride := getPodMountOptionsOverride(ctx, client, "default", "test-pod", "test-pvc")
	if podOverride != "-readcache, +writecache" {
		t.Errorf("Expected pod override '-readcache, +writecache', got '%s'", podOverride)
	}
}

// TestParsePodMountAnnotation_EmptyAnnotation tests empty annotation handling
func TestParsePodMountAnnotation_EmptyAnnotation(t *testing.T) {
	entries := parsePodMountAnnotation("")
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries for empty annotation, got %d", len(entries))
	}
}

// TestParsePodMountAnnotation_OnlyWhitespace tests whitespace-only annotation
func TestParsePodMountAnnotation_OnlyWhitespace(t *testing.T) {
	entries := parsePodMountAnnotation("   \n\n   \t   ")
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries for whitespace-only annotation, got %d", len(entries))
	}
}

// TestParsePodMountAnnotation_OnlyComments tests comment-only annotation
func TestParsePodMountAnnotation_OnlyComments(t *testing.T) {
	entries := parsePodMountAnnotation("# Comment 1\n# Comment 2\n# Comment 3")
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries for comment-only annotation, got %d", len(entries))
	}
}

// TestMountOptionOverride_ApplyToOptions_OverwriteValuedOption tests replacing valued options
func TestMountOptionOverride_ApplyToOptions_OverwriteValuedOption(t *testing.T) {
	opts := NewMountOptions([]string{"readahead_kb=16384"})
	exclusives := []mutuallyExclusiveMountOptionSet{}

	// Overwrite with new value
	override := MountOptionOverride("+readahead_kb=32768")
	result := override.ApplyToOptions(opts, exclusives)

	if result.getOptionValue("readahead_kb") != "32768" {
		t.Errorf("Expected 'readahead_kb' value to be updated to '32768', got '%s'", result.getOptionValue("readahead_kb"))
	}
}

// TestApplyToOptions_RemovalSurvivesDefaultsMerge is the case the exclusion mechanism exists for.
// The node defaults are merged UNDERNEATH the volume's options at mount time, so a "-opt" that
// only deleted the option from the volume's own set would be handed straight back by the defaults.
func TestApplyToOptions_RemovalSurvivesDefaultsMerge(t *testing.T) {
	defaults := NewMountOptionsFromString(NodeServerAdditionalMountOptions)
	if !defaults.hasOption(MountOptionSyncOnClose) {
		t.Fatalf("precondition: node defaults should carry %s, got '%s'", MountOptionSyncOnClose, defaults.String())
	}

	opts := NewMountOptionsFromString("readcache")
	opts = MountOptionOverride("-"+MountOptionSyncOnClose).ApplyToOptions(opts, nil)

	final := defaults.MergedWith(opts, nil)
	if final.hasOption(MountOptionSyncOnClose) {
		t.Errorf("Expected '%s' to stay removed after the defaults merge, got '%s'", MountOptionSyncOnClose, final.String())
	}
	if !final.hasOption(MountOptionWriteCache) {
		t.Errorf("Expected the other default '%s' to be kept, got '%s'", MountOptionWriteCache, final.String())
	}
}

// TestApplyToOptions_ReAddAfterRemovalWins covers "-opt" then "+opt", e.g. a PVC annotation
// removing an option and the pod annotation putting it back.
func TestApplyToOptions_ReAddAfterRemovalWins(t *testing.T) {
	defaults := NewMountOptionsFromString(NodeServerAdditionalMountOptions)

	opts := NewMountOptionsFromString("readcache")
	opts = MountOptionOverride("-"+MountOptionSyncOnClose).ApplyToOptions(opts, nil)
	opts = MountOptionOverride("+"+MountOptionSyncOnClose).ApplyToOptions(opts, nil)

	final := defaults.MergedWith(opts, nil)
	if !final.hasOption(MountOptionSyncOnClose) {
		t.Errorf("Expected '%s' to be re-added by the later '+', got '%s'", MountOptionSyncOnClose, final.String())
	}
}

// TestApplyToOptions_RemovalDoesNotLeakToSource guards the shared excludeOptions slice: an
// exclusion recorded on a derived value must not appear on the value it was derived from.
func TestApplyToOptions_RemovalDoesNotLeakToSource(t *testing.T) {
	base := NewMountOptionsFromString("readcache," + MountOptionSyncOnClose)
	derived := MountOptionOverride("-"+MountOptionSyncOnClose).ApplyToOptions(base, nil)

	if !base.hasOption(MountOptionSyncOnClose) {
		t.Errorf("Expected the source options to be untouched, got '%s'", base.String())
	}
	if derived.hasOption(MountOptionSyncOnClose) {
		t.Errorf("Expected the derived options to have dropped '%s', got '%s'", MountOptionSyncOnClose, derived.String())
	}
	if len(base.excludeOptions) != 0 {
		t.Errorf("Expected no exclusions recorded on the source, got %v", base.excludeOptions)
	}
}

// TestApplyToOptions_ValuedOptionRoundTrip checks the "=" split is respected by the exclusion
// bookkeeping, which keys on the option name rather than the whole "name=value" string.
func TestApplyToOptions_ValuedOptionRoundTrip(t *testing.T) {
	opts := NewMountOptionsFromString("inode_bits=32")
	opts = MountOptionOverride("-inode_bits").ApplyToOptions(opts, nil)
	if opts.hasOption("inode_bits") {
		t.Fatalf("Expected inode_bits removed, got '%s'", opts.String())
	}
	opts = MountOptionOverride("inode_bits=64").ApplyToOptions(opts, nil)
	if got := opts.getOptionValue("inode_bits"); got != "64" {
		t.Errorf("Expected inode_bits=64 after re-add, got '%s' (opts '%s')", got, opts.String())
	}
	if final := NewMountOptionsFromString("inode_bits=32").MergedWith(opts, nil); final.getOptionValue("inode_bits") != "64" {
		t.Errorf("Expected the re-added value to win the defaults merge, got '%s'", final.String())
	}
}

// exclusiveCacheOptions is the mutually exclusive set the driver falls back to when the chart
// configures none - see NewDriverConfig.
func exclusiveCacheOptions() []mutuallyExclusiveMountOptionSet {
	return []mutuallyExclusiveMountOptionSet{{MountOptionWriteCache, MountOptionCoherent, MountOptionReadCache}}
}

// mountOptionPipeline mirrors the order NodePublishVolume assembles mount options in, so a test
// can state a StorageClass setting plus annotation overrides and assert on what would actually
// be mounted:
//
//  1. the volume's options start from the volume context, i.e. the StorageClass mountOptions
//  2. the PVC override is applied, then the Pod override
//  3. the node defaults are merged UNDERNEATH all of it at mount time, in MountUnderlyingFS
func mountOptionPipeline(storageClassOpts string, overrides []string, exclusives []mutuallyExclusiveMountOptionSet) MountOptions {
	opts := getDefaultMountOptions()
	opts.Merge(NewMountOptionsFromString(storageClassOpts), exclusives)
	for _, o := range overrides {
		opts = MountOptionOverride(o).ApplyToOptions(opts, exclusives)
	}
	nodeDefaults := getDefaultMountOptions().MergedWith(NewMountOptionsFromString(NodeServerAdditionalMountOptions), exclusives)
	return nodeDefaults.MergedWith(opts, exclusives)
}

// TestMountOptionPipeline_MutualExclusivity pins down how the mutually exclusive cache options
// behave across the whole assembly: the driver default is writecache, a StorageClass or an
// override can displace it, and whichever of the three is applied last must be the only one left.
func TestMountOptionPipeline_MutualExclusivity(t *testing.T) {
	exclusives := exclusiveCacheOptions()

	for _, tc := range []struct {
		name         string
		storageClass string
		overrides    []string
		wantPresent  []string
		wantAbsent   []string
	}{
		{
			name:        "node default applies when nothing else asks",
			wantPresent: []string{MountOptionWriteCache, MountOptionSyncOnClose},
			wantAbsent:  []string{MountOptionReadCache, MountOptionCoherent},
		},
		{
			name:         "storageclass readcache displaces the default writecache",
			storageClass: MountOptionReadCache,
			wantPresent:  []string{MountOptionReadCache, MountOptionSyncOnClose},
			wantAbsent:   []string{MountOptionWriteCache, MountOptionCoherent},
		},
		{
			name:         "pod +writecache wins back over the storageclass readcache",
			storageClass: MountOptionReadCache,
			overrides:    []string{"+" + MountOptionWriteCache},
			wantPresent:  []string{MountOptionWriteCache},
			wantAbsent:   []string{MountOptionReadCache, MountOptionCoherent},
		},
		{
			name:         "pod -readcache drops the storageclass choice and falls back to the default",
			storageClass: MountOptionReadCache,
			overrides:    []string{"-" + MountOptionReadCache},
			wantPresent:  []string{MountOptionWriteCache},
			wantAbsent:   []string{MountOptionReadCache, MountOptionCoherent},
		},
		{
			name:         "-readcache,+writecache in one override",
			storageClass: MountOptionReadCache,
			overrides:    []string{"-" + MountOptionReadCache + ",+" + MountOptionWriteCache},
			wantPresent:  []string{MountOptionWriteCache},
			wantAbsent:   []string{MountOptionReadCache, MountOptionCoherent},
		},
		{
			name:         "+writecache,-readcache is order independent",
			storageClass: MountOptionReadCache,
			overrides:    []string{"+" + MountOptionWriteCache + ",-" + MountOptionReadCache},
			wantPresent:  []string{MountOptionWriteCache},
			wantAbsent:   []string{MountOptionReadCache, MountOptionCoherent},
		},
		{
			name:         "pod override wins over the pvc override",
			storageClass: MountOptionReadCache,
			overrides:    []string{"+" + MountOptionCoherent, "+" + MountOptionWriteCache},
			wantPresent:  []string{MountOptionWriteCache},
			wantAbsent:   []string{MountOptionReadCache, MountOptionCoherent},
		},
		{
			name:         "pod +readcache displaces a storageclass writecache",
			storageClass: MountOptionWriteCache,
			overrides:    []string{"+" + MountOptionReadCache},
			wantPresent:  []string{MountOptionReadCache},
			wantAbsent:   []string{MountOptionWriteCache, MountOptionCoherent},
		},
		{
			name:        "-writecache removes the default and leaves no cache option at all",
			overrides:   []string{"-" + MountOptionWriteCache},
			wantPresent: []string{MountOptionSyncOnClose},
			wantAbsent:  []string{MountOptionWriteCache, MountOptionReadCache, MountOptionCoherent},
		},
		{
			name:        "-writecache,+writecache puts the default back",
			overrides:   []string{"-" + MountOptionWriteCache + ",+" + MountOptionWriteCache},
			wantPresent: []string{MountOptionWriteCache},
			wantAbsent:  []string{MountOptionReadCache, MountOptionCoherent},
		},
		{
			name:        "-writecache,+readcache swaps the default for another of the set",
			overrides:   []string{"-" + MountOptionWriteCache + ",+" + MountOptionReadCache},
			wantPresent: []string{MountOptionReadCache},
			wantAbsent:  []string{MountOptionWriteCache, MountOptionCoherent},
		},
		{
			name:        "+coherent then -coherent falls back to the node default",
			overrides:   []string{"+" + MountOptionCoherent, "-" + MountOptionCoherent},
			wantPresent: []string{MountOptionWriteCache},
			wantAbsent:  []string{MountOptionCoherent, MountOptionReadCache},
		},
		{
			name:         "an exclusive option must not resurrect an excluded one",
			storageClass: MountOptionReadCache,
			overrides:    []string{"-" + MountOptionWriteCache + ",+" + MountOptionCoherent},
			wantPresent:  []string{MountOptionCoherent},
			wantAbsent:   []string{MountOptionWriteCache, MountOptionReadCache},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mountOptionPipeline(tc.storageClass, tc.overrides, exclusives)
			for _, want := range tc.wantPresent {
				if !got.hasOption(want) {
					t.Errorf("Expected '%s' to be present, got '%s'", want, got.String())
				}
			}
			for _, notWant := range tc.wantAbsent {
				if got.hasOption(notWant) {
					t.Errorf("Expected '%s' to be absent, got '%s'", notWant, got.String())
				}
			}
			// whichever cache option survived, it must be the only one of the set
			var surviving []string
			for _, o := range []string{MountOptionWriteCache, MountOptionCoherent, MountOptionReadCache} {
				if got.hasOption(o) {
					surviving = append(surviving, o)
				}
			}
			if len(surviving) > 1 {
				t.Errorf("Expected at most one of the mutually exclusive set, got %v in '%s'", surviving, got.String())
			}
		})
	}
}

// --- TestMountOptionPipeline_FullPublishScenario -----------------------------------------------
//
// mountOptionPipeline above models only the merges. It never calls pruneUnsupportedMountOptions,
// so it cannot show whether "ro" supplied via an override is actually refused, or whether
// sync_on_close capability pruning would mask a "-sync_on_close" exclusion. This test drives the
// REAL methods NodePublishVolume calls, in the same order, against a real *Volume/*NodeServer:
//
//  1. volume.setMountOptions(scOpts) then volume.pruneUnsupportedMountOptions
//  2. volume.mountOptions.Merge(VolumeCapability mount flags) - empty in this scenario
//  3. ns.applyMountOptionsOverridesToVolume (PVC override then Pod override), via the actual
//     function reading actual PVC/Pod objects out of a fake controller-runtime client
//  4. volume.pruneUnsupportedMountOptions again (this is where a "ro" override must be refused)
//  5. if readOnly: volume.mountOptions.Merge(NewMountOptions([]string{"ro"}).ExcludeOption("rw"))
//  6. the MountUnderlyingFS line: withUnsupportedMountOptionsPruned(defaults.MergedWith(volume options))
//
// No production code is modified or reimplemented here beyond gluing these real calls together in
// the real order; step 6 stops short of calling MountUnderlyingFS itself only because that also
// invokes the mounter, which would require a full AnyMounter fake with real mount side effects.
func newFullPublishScenarioNodeServer() *NodeServer {
	return &NodeServer{config: &DriverConfig{mutuallyExclusiveOptions: exclusiveCacheOptions()}}
}

// fullPublishScenarioParams bundles what varies across the scenarios below.
type fullPublishScenarioParams struct {
	storageClassOpts string
	pvcOverride      string // annotation value for weka.io/mount-options-override (applies to all pods)
	podOverride      string // modifiers only; wrapped as "test-pvc: <podOverride>" for weka.io/mount-options-overrides
	apiClient        *apiclient.ApiClient
	readOnly         bool
}

// runFullPublishScenario builds a real *NodeServer and *Volume and pushes them through the exact
// 6-step sequence NodeServer.NodePublishVolume uses (nodeserver.go ~331-407), calling the real
// production methods at each step, and returns the MountOptions that would actually be passed to
// the mounter.
func runFullPublishScenario(t *testing.T, p fullPublishScenarioParams) MountOptions {
	t.Helper()
	ctx := context.Background()
	ns := newFullPublishScenarioNodeServer()

	volume := &Volume{
		mountOptions: getDefaultMountOptions(),
		apiClient:    p.apiClient,
		server:       ns,
	}
	// mirrors initMountOptions (volumeconstructors.go), which every real Volume goes through
	volume.pruneUnsupportedMountOptions(ctx)

	// --- step 1: nodeserver.go ~331-339 ---
	volume.setMountOptions(ctx, NewMountOptionsFromString(p.storageClassOpts))
	volume.pruneUnsupportedMountOptions(ctx)

	// --- step 2: nodeserver.go ~373-374 (no VolumeCapability mount flags in this scenario) ---
	volume.mountOptions.Merge(NewMountOptionsFromString(""), ns.getConfig().mutuallyExclusiveOptions)

	// --- step 3: nodeserver.go ~376-394, via the real applyMountOptionsOverridesToVolume ---
	const pvcNamespace, pvcName = "default", "test-pvc"
	const podNamespace, podName = "default", "test-pod"

	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: pvcNamespace},
	}
	if p.pvcOverride != "" {
		pvc.Annotations = map[string]string{PvcMountOptionOverrideAnnotation: p.pvcOverride}
	}
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: podNamespace},
	}
	if p.podOverride != "" {
		pod.Annotations = map[string]string{PodMountOptionOverrideAnnotation: pvcName + ": " + p.podOverride}
	}
	crClient := fakeClient.NewClientBuilder().WithObjects(pvc, pod).Build()

	req := &csi.NodePublishVolumeRequest{
		VolumeContext: map[string]string{
			"mountOptions":               p.storageClassOpts,
			VolumeContextPvcNameKey:      pvcName,
			VolumeContextPvcNamespaceKey: pvcNamespace,
			VolumeContextPodNameKey:      podName,
			VolumeContextPodNamespaceKey: podNamespace,
		},
	}
	if err := ns.applyMountOptionsOverridesToVolume(ctx, req, volume, crClient); err != nil {
		t.Fatalf("applyMountOptionsOverridesToVolume returned an error: %v", err)
	}

	// --- step 4: nodeserver.go ~399 ---
	volume.pruneUnsupportedMountOptions(ctx)

	// --- step 5: nodeserver.go ~401-407 ---
	if p.readOnly {
		roMountOptions := NewMountOptions([]string{"ro"}).ExcludeOption("rw")
		volume.mountOptions.Merge(roMountOptions, ns.getConfig().mutuallyExclusiveOptions)
	}

	// --- step 6: the MountUnderlyingFS line (volume.go:1048) ---
	final := volume.withUnsupportedMountOptionsPruned(ctx,
		ns.getDefaultMountOptions().MergedWith(volume.getMountOptions(ctx), ns.getConfig().mutuallyExclusiveOptions))
	return final
}

// supportingApiClient returns an *apiclient.ApiClient whose compatibility map reports
// sync_on_close as supported, without any live cluster connection: CompatibilityMap is populated
// directly, exactly as fetchClusterInfo would populate it after a real login.
func supportingApiClient() *apiclient.ApiClient {
	return &apiclient.ApiClient{CompatibilityMap: &apiclient.WekaCompatibilityMap{SyncOnCloseMountOption: true}}
}

func TestMountOptionPipeline_FullPublishScenario(t *testing.T) {
	assertExpected := func(t *testing.T, final MountOptions, present, absent []string) {
		t.Helper()
		t.Logf("final mount options: %q", final.String())
		for _, want := range present {
			if !final.hasOption(want) {
				t.Errorf("Expected %q to be present, got %q", want, final.String())
			}
		}
		for _, notWant := range absent {
			if final.hasOption(notWant) {
				t.Errorf("Expected %q to be absent, got %q", notWant, final.String())
			}
		}
	}

	// The exact user scenario, expressed in every equivalent way it can arrive: the whole
	// override on the PVC annotation or split across PVC+Pod, and each of those in bare form
	// (as the user actually wrote it) and in the fully "+"-prefixed form. All four must produce
	// the identical final mounted set.
	scenarioVariants := map[string]fullPublishScenarioParams{
		"bare_single_pvc": {
			storageClassOpts: MountOptionWriteCache,
			pvcOverride:      "-sync_on_close,ro,readcache",
			apiClient:        supportingApiClient(),
		},
		"prefixed_single_pvc": {
			storageClassOpts: MountOptionWriteCache,
			pvcOverride:      "-sync_on_close,+ro,+readcache",
			apiClient:        supportingApiClient(),
		},
		"bare_split_pvc_pod": {
			storageClassOpts: MountOptionWriteCache,
			pvcOverride:      "-sync_on_close",
			podOverride:      "ro,readcache",
			apiClient:        supportingApiClient(),
		},
		"prefixed_split_pvc_pod": {
			storageClassOpts: MountOptionWriteCache,
			pvcOverride:      "-sync_on_close",
			podOverride:      "+ro,+readcache",
			apiClient:        supportingApiClient(),
		},
	}

	var referenceFinal string
	for _, name := range []string{"bare_single_pvc", "prefixed_single_pvc", "bare_split_pvc_pod", "prefixed_split_pvc_pod"} {
		name := name
		t.Run(name, func(t *testing.T) {
			final := runFullPublishScenario(t, scenarioVariants[name])
			assertExpected(t, final,
				[]string{MountOptionReadCache},
				[]string{MountOptionWriteCache, MountOptionCoherent, MountOptionSyncOnClose, MountOptionReadOnly})
			if referenceFinal == "" {
				referenceFinal = final.String()
			} else if final.String() != referenceFinal {
				t.Errorf("Expected the same final mount options regardless of override form/placement: got %q, want %q (reference case)", final.String(), referenceFinal)
			}
		})
	}

	// Control (c): a genuine readonly attachment with NO override must still end up with "ro" -
	// proving the refusal in step 4 (which drops a USER-supplied "ro") does not clobber the "ro"
	// the driver adds itself in step 5, which runs after it.
	t.Run("control_readonly_attachment_without_override", func(t *testing.T) {
		final := runFullPublishScenario(t, fullPublishScenarioParams{
			storageClassOpts: MountOptionWriteCache,
			apiClient:        supportingApiClient(),
			readOnly:         true,
		})
		assertExpected(t, final, []string{MountOptionReadOnly, MountOptionWriteCache}, nil)
	})

	// A readonly attachment combined with a "-ro" override must STILL mount readonly. This is the
	// companion to control (c), and the case that control (c) alone does not reach: "-ro" records
	// an exclusion of "ro", and because Merge applies exclusions after additions, that exclusion
	// outlived the refusal step and deleted the "ro" the readonly path had added - at the defaults
	// merge, after every assertion a publish-time test would naturally make. The underlying
	// filesystem was then mounted writable. The refusal step clears the exclusion as well as the
	// option, so both forms of user input about "ro" are refused in the one place that owns that
	// policy.
	for name, override := range map[string]fullPublishScenarioParams{
		"readonly_attachment_with_pvc_ro_removal": {pvcOverride: "-" + MountOptionReadOnly},
		"readonly_attachment_with_pod_ro_removal": {podOverride: "-" + MountOptionReadOnly},
		"readonly_attachment_with_ro_removal_alongside_another_modifier": {
			pvcOverride: "-" + MountOptionReadOnly + ",+" + MountOptionReadCache,
		},
	} {
		t.Run(name, func(t *testing.T) {
			final := runFullPublishScenario(t, fullPublishScenarioParams{
				storageClassOpts: MountOptionWriteCache,
				pvcOverride:      override.pvcOverride,
				podOverride:      override.podOverride,
				apiClient:        supportingApiClient(),
				readOnly:         true,
			})
			assertExpected(t, final, []string{MountOptionReadOnly}, nil)
		})
	}

	// The mirror of the above: with no readonly attachment, a "-ro" override must NOT leave "ro"
	// behind. Clearing the exclusion must not amount to granting the "ro" that step 4 refuses.
	t.Run("ro_removal_without_a_readonly_attachment_adds_no_ro", func(t *testing.T) {
		final := runFullPublishScenario(t, fullPublishScenarioParams{
			storageClassOpts: MountOptionWriteCache,
			pvcOverride:      "-" + MountOptionReadOnly,
			apiClient:        supportingApiClient(),
			readOnly:         false,
		})
		assertExpected(t, final, []string{MountOptionWriteCache}, []string{MountOptionReadOnly})
	})

	// Control (d): isolate the exclusion mechanism from capability pruning. The apiClient here
	// DOES support sync_on_close (SupportsSyncOnCloseMountOption() == true), so
	// withUnsupportedMountOptionsPruned would NOT drop it on capability grounds. If sync_on_close
	// is still absent from the final set, that is only because the "-sync_on_close" override's
	// ExcludeOption survived the defaults merge - the mechanism under test, not a side effect of
	// an unsupported cluster version. This was feasible: apiclient.ApiClient.CompatibilityMap is a
	// plain exported field, so a "supporting" client needs no live cluster or login.
	t.Run("control_exclusion_isolated_with_supporting_apiclient", func(t *testing.T) {
		final := runFullPublishScenario(t, fullPublishScenarioParams{
			storageClassOpts: MountOptionWriteCache,
			pvcOverride:      "-sync_on_close",
			apiClient:        supportingApiClient(),
		})
		assertExpected(t, final, []string{MountOptionWriteCache}, []string{MountOptionSyncOnClose})
	})

	// Sanity companion to control (d): same supporting apiClient, no override at all - proves
	// sync_on_close is normally present with this fixture, so its absence above is caused by the
	// exclusion and not by some accident of the "supporting" apiClient fixture itself.
	t.Run("sanity_supporting_apiclient_keeps_sync_on_close_without_override", func(t *testing.T) {
		final := runFullPublishScenario(t, fullPublishScenarioParams{
			storageClassOpts: MountOptionWriteCache,
			apiClient:        supportingApiClient(),
		})
		assertExpected(t, final, []string{MountOptionWriteCache, MountOptionSyncOnClose}, nil)
	})

	// Contrast for control (d): with apiClient == nil (cluster version unknown), sync_on_close is
	// dropped by CAPABILITY pruning alone, with no override involved at all. This is the mechanism
	// control (d) is designed to rule out as the explanation for the exclusion's effect.
	t.Run("contrast_capability_pruning_drops_sync_on_close_without_any_override", func(t *testing.T) {
		final := runFullPublishScenario(t, fullPublishScenarioParams{
			storageClassOpts: MountOptionWriteCache,
			apiClient:        nil,
		})
		assertExpected(t, final, []string{MountOptionWriteCache}, []string{MountOptionSyncOnClose})
	})
}

// --- TestMountOptionOverride_BarePrefixEquivalence ---------------------------------------------
//
// Pins down, as a guaranteed contract rather than an incidental observation, that a bare option
// and its "+"-prefixed form are indistinguishable in every respect: ApplyToOptions routes both
// through the exact same addOverride call. This compares the full resulting MountOptions (both
// the customOptions map and the excludeOptions bookkeeping) rather than merely checking that both
// contain the option, so a future divergence between the two branches would be caught even if it
// only affected exclusion bookkeeping rather than the visible option set.
func assertMountOptionsIdentical(t *testing.T, bare, prefixed MountOptions, label string) {
	t.Helper()
	bareExclude := append([]string(nil), bare.excludeOptions...)
	prefixedExclude := append([]string(nil), prefixed.excludeOptions...)
	sort.Strings(bareExclude)
	sort.Strings(prefixedExclude)

	if !reflect.DeepEqual(bare.customOptions, prefixed.customOptions) {
		t.Errorf("%s: customOptions differ:\n  bare:     %v\n  prefixed: %v", label, bare.customOptions, prefixed.customOptions)
	}
	if !reflect.DeepEqual(bareExclude, prefixedExclude) {
		t.Errorf("%s: excludeOptions differ:\n  bare:     %v\n  prefixed: %v", label, bareExclude, prefixedExclude)
	}
	// Belt-and-suspenders: the normalized String() form must also match.
	if bare.String() != prefixed.String() {
		t.Errorf("%s: String() differs: bare=%q prefixed=%q", label, bare.String(), prefixed.String())
	}
}

func TestMountOptionOverride_BarePrefixEquivalence(t *testing.T) {
	exclusives := exclusiveCacheOptions()

	for _, tc := range []struct {
		name     string
		start    MountOptions
		bare     string
		prefixed string
	}{
		{
			name:     "plain option",
			start:    NewMountOptionsFromString("readcache"),
			bare:     MountOptionReadCache,
			prefixed: "+" + MountOptionReadCache,
		},
		{
			name:     "option carrying a value",
			start:    NewMountOptionsFromString(""),
			bare:     "inode_bits=64",
			prefixed: "+inode_bits=64",
		},
		{
			name:     "mixed with a removal, override order 1 (- then bare/+)",
			start:    NewMountOptionsFromString(NodeServerAdditionalMountOptions), // writecache,sync_on_close
			bare:     "-sync_on_close,readcache",
			prefixed: "-sync_on_close,+readcache",
		},
		{
			name:     "mixed with a removal, override order 2 (bare/+ then -)",
			start:    NewMountOptionsFromString(NodeServerAdditionalMountOptions),
			bare:     "readcache,-sync_on_close",
			prefixed: "+readcache,-sync_on_close",
		},
		{
			name:     "re-add after removal (UnexcludeOption path)",
			start:    NewMountOptionsFromString(MountOptionWriteCache),
			bare:     "-writecache,writecache",
			prefixed: "-writecache,+writecache",
		},
		{
			name:     "mutually exclusive set: displacing writecache",
			start:    NewMountOptionsFromString(MountOptionWriteCache),
			bare:     MountOptionCoherent,
			prefixed: "+" + MountOptionCoherent,
		},
		{
			name:     "whitespace tolerance around the modifier",
			start:    NewMountOptionsFromString(""),
			bare:     " readcache ",
			prefixed: " +readcache ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bareResult := MountOptionOverride(tc.bare).ApplyToOptions(tc.start, exclusives)
			prefixedResult := MountOptionOverride(tc.prefixed).ApplyToOptions(tc.start, exclusives)
			assertMountOptionsIdentical(t, bareResult, prefixedResult, tc.name)

			// Sanity: this pair must actually add the option, so the comparison above isn't
			// trivially passing on two empty/unaffected results.
			if !bareResult.hasOption("readcache") && !bareResult.hasOption("inode_bits") &&
				!bareResult.hasOption("writecache") && !bareResult.hasOption("coherent") {
				t.Fatalf("%s: test setup did not actually exercise an add - bareResult = %q", tc.name, bareResult.String())
			}
		})
	}

	// The exact user scenario override, compared end to end: the whole string as the user wrote
	// it (one bare modifier, two bare options) versus every modifier explicitly "+"-prefixed.
	t.Run("full user scenario string, bare vs fully prefixed", func(t *testing.T) {
		start := NewMountOptionsFromString(NodeServerAdditionalMountOptions) // writecache,sync_on_close
		bareResult := MountOptionOverride("-sync_on_close,ro,readcache").ApplyToOptions(start, exclusives)
		prefixedResult := MountOptionOverride("-sync_on_close,+ro,+readcache").ApplyToOptions(start, exclusives)
		assertMountOptionsIdentical(t, bareResult, prefixedResult, "full user scenario string")
	})
}
