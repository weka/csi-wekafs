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
