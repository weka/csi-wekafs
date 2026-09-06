// Package convert strips server-managed metadata from Kubernetes objects and rewrites
// dynamically provisioned volumes into the static form needed to recreate them elsewhere.
//
// Objects are handled as unstructured maps rather than typed structs. Typed structs would
// force zero values such as `status: {}` and `creationTimestamp: null` into the output;
// working with maps lets absent fields stay absent, which is what makes the exported YAML
// readable and directly applicable.
package convert

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// serverManagedMetadata are metadata fields the API server owns. Carrying any of them to a
// new cluster is at best noise and at worst fatal: a stale resourceVersion is rejected
// outright, and a uid silently breaks binding.
var serverManagedMetadata = []string{
	"uid",
	"resourceVersion",
	"generation",
	"creationTimestamp",
	"deletionTimestamp",
	"deletionGracePeriodSeconds",
	"managedFields",
	"selfLink",
	"ownerReferences",
	"finalizers",
}

// serverManagedAnnotations are annotations written by controllers, which must be re-derived
// on the target cluster rather than imported.
var serverManagedAnnotations = []string{
	"kubectl.kubernetes.io/last-applied-configuration",
	"control-plane.alpha.kubernetes.io/leader",
}

// ToUnstructured converts a typed object, tagging it with its GroupVersionKind. Typed
// clients strip apiVersion and kind from what they return, but both are required for the
// exported YAML to be applicable.
func ToUnstructured(obj runtime.Object, apiVersion, kind string) (*unstructured.Unstructured, error) {
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("converting %s to unstructured: %w", kind, err)
	}
	u := &unstructured.Unstructured{Object: raw}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	return u, nil
}

// Neat removes server-managed metadata, status, and controller-written annotations, leaving
// only what is needed to recreate the object.
func Neat(u *unstructured.Unstructured) {
	unstructured.RemoveNestedField(u.Object, "status")

	for _, field := range serverManagedMetadata {
		unstructured.RemoveNestedField(u.Object, "metadata", field)
	}
	removeAnnotations(u, serverManagedAnnotations...)

	// An empty annotations or labels map is not wrong, only noise.
	pruneEmptyMap(u, "metadata", "annotations")
	pruneEmptyMap(u, "metadata", "labels")
}

// removeAnnotations deletes the named annotations if present.
func removeAnnotations(u *unstructured.Unstructured, names ...string) {
	annotations, found, err := unstructured.NestedStringMap(u.Object, "metadata", "annotations")
	if err != nil || !found {
		return
	}
	changed := false
	for _, name := range names {
		if _, ok := annotations[name]; ok {
			delete(annotations, name)
			changed = true
		}
	}
	if !changed {
		return
	}
	if len(annotations) == 0 {
		unstructured.RemoveNestedField(u.Object, "metadata", "annotations")
		return
	}
	// The error is unreachable: the value came from this same object.
	_ = unstructured.SetNestedStringMap(u.Object, annotations, "metadata", "annotations")
}

func pruneEmptyMap(u *unstructured.Unstructured, fields ...string) {
	value, found, err := unstructured.NestedMap(u.Object, fields...)
	if err != nil || !found {
		return
	}
	if len(value) == 0 {
		unstructured.RemoveNestedField(u.Object, fields...)
	}
}

// hasAnnotation reports whether the object carries the named annotation.
func hasAnnotation(u *unstructured.Unstructured, name string) bool {
	annotations, found, err := unstructured.NestedStringMap(u.Object, "metadata", "annotations")
	if err != nil || !found {
		return false
	}
	_, ok := annotations[name]
	return ok
}
