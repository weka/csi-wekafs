package convert

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// AnnProvisionedBy marks a PersistentVolume as owned by a dynamic provisioner.
//
// Removing it is load-bearing for data safety, not cosmetic tidying. external-provisioner
// only reclaims a PersistentVolume whose provisioned-by annotation names it; a volume
// without the annotation is ignored no matter what its reclaim policy says. Exported
// volumes keep their original reclaim policy, so this annotation is the single thing
// standing between a restored cluster and a `kubectl delete pvc` that destroys the Weka
// data the export was taken to protect.
//
// TestExportedPVIsNotReclaimable pins this behaviour. Do not reinstate the annotation
// without also forcing the reclaim policy to Retain.
const AnnProvisionedBy = "pv.kubernetes.io/provisioned-by"

// provisionerAnnotationsPV are written onto PersistentVolumes by the provisioning stack.
var provisionerAnnotationsPV = []string{
	AnnProvisionedBy,
	"pv.kubernetes.io/bound-by-controller",
	"volume.kubernetes.io/provisioner-deletion-secret-name",
	"volume.kubernetes.io/provisioner-deletion-secret-namespace",
}

// provisionerAnnotationsPVC are written onto PersistentVolumeClaims during binding. They
// must be re-derived by the target cluster, otherwise a claim can appear bound to a volume
// that does not exist there.
var provisionerAnnotationsPVC = []string{
	"pv.kubernetes.io/bind-completed",
	"pv.kubernetes.io/bound-by-controller",
	"volume.beta.kubernetes.io/storage-provisioner",
	"volume.kubernetes.io/storage-provisioner",
	"volume.kubernetes.io/selected-node",
}

// AttrProvisionerIdentity is injected into volumeAttributes by external-provisioner and
// identifies the provisioner instance that created the volume. It is meaningless on another
// cluster, and the Weka driver does not read it.
const AttrProvisionerIdentity = "storage.kubernetes.io/csiProvisionerIdentity"

// StaticPV rewrites a dynamically provisioned PersistentVolume into static form.
//
// The volume handle is never touched: it is the driver's opaque identifier for the data on
// the Weka cluster, and rewriting it would repoint the volume. See pkg/volumeid.
func StaticPV(u *unstructured.Unstructured) error {
	Neat(u)
	removeAnnotations(u, provisionerAnnotationsPV...)

	// A claimRef pre-binds the volume to its claim, which is what preserves the original
	// PVC-to-PV pairing across a restore. Its uid, however, refers to a claim that will not
	// exist on the target cluster; a claimRef carrying a stale uid never binds and the
	// volume stays Available forever.
	if _, found, _ := unstructured.NestedMap(u.Object, "spec", "claimRef"); found {
		unstructured.RemoveNestedField(u.Object, "spec", "claimRef", "uid")
		unstructured.RemoveNestedField(u.Object, "spec", "claimRef", "resourceVersion")
	}

	return pruneVolumeAttributes(u)
}

// pruneVolumeAttributes drops provisioner bookkeeping from spec.csi.volumeAttributes while
// leaving every driver-meaningful attribute intact.
func pruneVolumeAttributes(u *unstructured.Unstructured) error {
	attrs, found, err := unstructured.NestedStringMap(u.Object, "spec", "csi", "volumeAttributes")
	if err != nil {
		return fmt.Errorf("reading spec.csi.volumeAttributes: %w", err)
	}
	if !found {
		return nil
	}
	if _, ok := attrs[AttrProvisionerIdentity]; !ok {
		return nil
	}
	delete(attrs, AttrProvisionerIdentity)
	if len(attrs) == 0 {
		unstructured.RemoveNestedField(u.Object, "spec", "csi", "volumeAttributes")
		return nil
	}
	return unstructured.SetNestedStringMap(u.Object, attrs, "spec", "csi", "volumeAttributes")
}

// StaticPVC rewrites a PersistentVolumeClaim into static form, pinning it to the volume it
// is currently bound to.
//
// Setting spec.volumeName is what makes the claim static: on the target cluster it binds to
// that exact volume instead of triggering a fresh provision against the StorageClass, which
// would create new, empty storage and leave the original data orphaned.
func StaticPVC(u *unstructured.Unstructured, volumeName string) error {
	Neat(u)
	removeAnnotations(u, provisionerAnnotationsPVC...)

	if volumeName == "" {
		return fmt.Errorf("claim %s/%s is not bound to a volume", u.GetNamespace(), u.GetName())
	}
	if err := unstructured.SetNestedField(u.Object, volumeName, "spec", "volumeName"); err != nil {
		return fmt.Errorf("setting spec.volumeName on claim %s/%s: %w", u.GetNamespace(), u.GetName(), err)
	}
	return nil
}

// NeatStorageClass strips server-managed metadata from a StorageClass. StorageClasses need
// no structural rewriting: a static PersistentVolume references its class only by name, but
// the class must still exist on the target for the claim's storageClassName to match.
func NeatStorageClass(u *unstructured.Unstructured) {
	Neat(u)
}
