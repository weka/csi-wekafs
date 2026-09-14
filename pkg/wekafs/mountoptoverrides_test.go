package wekafs

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
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
