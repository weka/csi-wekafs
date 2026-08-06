package convert

import (
	"encoding/base64"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// dynamicPV mirrors what external-provisioner actually leaves on a PersistentVolume it
// created, including the fields that must not survive an export.
func dynamicPV() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolume",
		"metadata": map[string]any{
			"name":              "pvc-8f3a1c2e",
			"uid":               "1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed",
			"resourceVersion":   "84213",
			"generation":        int64(1),
			"creationTimestamp": "2026-08-05T10:00:00Z",
			"managedFields":     []any{map[string]any{"manager": "csi-provisioner"}},
			"finalizers":        []any{"kubernetes.io/pv-protection"},
			"annotations": map[string]any{
				AnnProvisionedBy: "csi.weka.io",
				"volume.kubernetes.io/provisioner-deletion-secret-name":      "csi-wekafs-api-secret",
				"volume.kubernetes.io/provisioner-deletion-secret-namespace": "csi-wekafs",
				"keep.example.com/owner":                                     "team-a",
			},
		},
		"spec": map[string]any{
			"capacity":                      map[string]any{"storage": "1Gi"},
			"accessModes":                   []any{"ReadWriteMany"},
			"persistentVolumeReclaimPolicy": "Delete",
			"storageClassName":              "storageclass-wekafs-dir-api",
			"volumeMode":                    "Filesystem",
			"claimRef": map[string]any{
				"apiVersion":      "v1",
				"kind":            "PersistentVolumeClaim",
				"name":            "pvc-wekafs-dir-api",
				"namespace":       "default",
				"uid":             "9f8e7d6c-5b4a-3210-fedc-ba9876543210",
				"resourceVersion": "84200",
			},
			"csi": map[string]any{
				"driver":       "csi.weka.io",
				"volumeHandle": "weka/v2/testfs//csi-volumes/vol-abc",
				"volumeAttributes": map[string]any{
					AttrProvisionerIdentity: "1754380800000-8081-csi.weka.io",
					"filesystemName":        "testfs",
					"volumeType":            "weka/v2",
				},
				"nodePublishSecretRef": map[string]any{"name": "csi-wekafs-api-secret", "namespace": "csi-wekafs"},
			},
		},
		"status": map[string]any{"phase": "Bound"},
	}}
}

func TestStaticPVStripsServerManagedFields(t *testing.T) {
	pv := dynamicPV()
	if !hasAnnotation(pv, AnnProvisionedBy) {
		t.Fatal("fixture is not recognised as dynamically provisioned")
	}
	if err := StaticPV(pv); err != nil {
		t.Fatalf("StaticPV returned error: %v", err)
	}

	for _, field := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "managedFields", "finalizers"} {
		if _, found, _ := unstructured.NestedFieldNoCopy(pv.Object, "metadata", field); found {
			t.Errorf("metadata.%s survived the conversion", field)
		}
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(pv.Object, "status"); found {
		t.Error("status survived the conversion")
	}
}

// TestExportedPVIsNotReclaimable is the data-safety guarantee described on AnnProvisionedBy.
//
// v1 preserves the original reclaim policy, so a restored volume can still say
// "Delete". That is only safe because external-provisioner ignores volumes it does not
// recognise as its own. If this test ever fails, a restored cluster can destroy live Weka
// data on `kubectl delete pvc`.
func TestExportedPVIsNotReclaimable(t *testing.T) {
	pv := dynamicPV()
	if err := StaticPV(pv); err != nil {
		t.Fatalf("StaticPV returned error: %v", err)
	}

	if hasAnnotation(pv, AnnProvisionedBy) {
		t.Fatalf("%s survived the conversion: a restored PVC deletion would destroy Weka data", AnnProvisionedBy)
	}
	// Confirm the premise of the guarantee: the policy really is still Delete.
	policy, _, _ := unstructured.NestedString(pv.Object, "spec", "persistentVolumeReclaimPolicy")
	if policy != "Delete" {
		t.Fatalf("fixture no longer exercises the risky case: reclaim policy is %q", policy)
	}
}

// TestStaticPVPreservesVolumeHandleExactly guards the identifier that locates the data on
// the Weka cluster, including its non-normalised double separator.
func TestStaticPVPreservesVolumeHandleExactly(t *testing.T) {
	pv := dynamicPV()
	if err := StaticPV(pv); err != nil {
		t.Fatalf("StaticPV returned error: %v", err)
	}
	handle, _, _ := unstructured.NestedString(pv.Object, "spec", "csi", "volumeHandle")
	if handle != "weka/v2/testfs//csi-volumes/vol-abc" {
		t.Errorf("volumeHandle = %q, want it byte-identical to the source", handle)
	}
}

// TestStaticPVStripsClaimRefUID covers the failure that leaves a restored volume stuck in
// Available forever while its claim stays Pending.
func TestStaticPVStripsClaimRefUID(t *testing.T) {
	pv := dynamicPV()
	if err := StaticPV(pv); err != nil {
		t.Fatalf("StaticPV returned error: %v", err)
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(pv.Object, "spec", "claimRef", "uid"); found {
		t.Error("claimRef.uid survived: the restored volume would never bind")
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(pv.Object, "spec", "claimRef", "resourceVersion"); found {
		t.Error("claimRef.resourceVersion survived")
	}
	// The binding itself must be kept, otherwise the original pairing is lost.
	name, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "name")
	ns, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "namespace")
	if name != "pvc-wekafs-dir-api" || ns != "default" {
		t.Errorf("claimRef identity lost: got %s/%s", ns, name)
	}
}

func TestStaticPVPrunesProvisionerAttributesButKeepsDriverOnes(t *testing.T) {
	pv := dynamicPV()
	if err := StaticPV(pv); err != nil {
		t.Fatalf("StaticPV returned error: %v", err)
	}
	attrs, found, _ := unstructured.NestedStringMap(pv.Object, "spec", "csi", "volumeAttributes")
	if !found {
		t.Fatal("volumeAttributes were removed entirely")
	}
	if _, ok := attrs[AttrProvisionerIdentity]; ok {
		t.Error("provisioner identity survived the conversion")
	}
	if attrs["filesystemName"] != "testfs" || attrs["volumeType"] != "weka/v2" {
		t.Errorf("driver-meaningful attributes were lost: %v", attrs)
	}
}

func TestStaticPVKeepsUserAnnotations(t *testing.T) {
	pv := dynamicPV()
	if err := StaticPV(pv); err != nil {
		t.Fatalf("StaticPV returned error: %v", err)
	}
	annotations, _, _ := unstructured.NestedStringMap(pv.Object, "metadata", "annotations")
	if annotations["keep.example.com/owner"] != "team-a" {
		t.Errorf("user annotation was dropped: %v", annotations)
	}
	if _, ok := annotations["volume.kubernetes.io/provisioner-deletion-secret-name"]; ok {
		t.Error("provisioner deletion secret annotation survived")
	}
}

func boundPVC() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata": map[string]any{
			"name":            "pvc-wekafs-dir-api",
			"namespace":       "default",
			"uid":             "9f8e7d6c-5b4a-3210-fedc-ba9876543210",
			"resourceVersion": "84200",
			"finalizers":      []any{"kubernetes.io/pvc-protection"},
			"annotations": map[string]any{
				"pv.kubernetes.io/bind-completed":               "yes",
				"pv.kubernetes.io/bound-by-controller":          "yes",
				"volume.beta.kubernetes.io/storage-provisioner": "csi.weka.io",
				"volume.kubernetes.io/storage-provisioner":      "csi.weka.io",
			},
		},
		"spec": map[string]any{
			"accessModes":      []any{"ReadWriteMany"},
			"resources":        map[string]any{"requests": map[string]any{"storage": "1Gi"}},
			"storageClassName": "storageclass-wekafs-dir-api",
			"volumeMode":       "Filesystem",
		},
		"status": map[string]any{"phase": "Bound"},
	}}
}

// TestStaticPVCPinsVolumeName covers the difference between restoring a claim and silently
// provisioning brand new empty storage next to the original data.
func TestStaticPVCPinsVolumeName(t *testing.T) {
	pvc := boundPVC()
	if err := StaticPVC(pvc, "pvc-8f3a1c2e"); err != nil {
		t.Fatalf("StaticPVC returned error: %v", err)
	}
	name, found, _ := unstructured.NestedString(pvc.Object, "spec", "volumeName")
	if !found || name != "pvc-8f3a1c2e" {
		t.Errorf("spec.volumeName = %q (found=%v), want pvc-8f3a1c2e", name, found)
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(pvc.Object, "metadata", "annotations"); found {
		t.Error("binding annotations survived: the claim would appear pre-bound on the target")
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(pvc.Object, "metadata", "finalizers"); found {
		t.Error("pvc-protection finalizer survived")
	}
}

func TestStaticPVCRejectsUnboundClaim(t *testing.T) {
	if err := StaticPVC(boundPVC(), ""); err == nil {
		t.Error("an unbound claim was accepted")
	}
}

func wekaSecret() *unstructured.Unstructured {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": "csi-wekafs-api-secret", "namespace": "csi-wekafs"},
		"type":       "Opaque",
		"data": map[string]any{
			"username":     enc("admin"),
			"password":     enc("s3cr3t"),
			"organization": enc("Root"),
			"endpoints":    enc("172.31.41.54:14000"),
			"scheme":       enc("https"),
			"nfsTargetIps": "",
			"kmsVaultSecretIdForFilesystemEncryption": enc("vault-secret-id"),
		},
	}}
}

// TestRedactSecretScrubsCredentialsButKeepsMigrationFields covers the CLI default: an
// operator must be able to read an export to plan endpoint and organization changes without
// the archive carrying live credentials.
func TestRedactSecretScrubsCredentialsButKeepsMigrationFields(t *testing.T) {
	secret := wekaSecret()
	redacted, err := RedactSecret(secret)
	if err != nil {
		t.Fatalf("RedactSecret returned error: %v", err)
	}
	if len(redacted) != 2 {
		t.Errorf("redacted %v, want password and the vault secret id", redacted)
	}

	data, _, _ := unstructured.NestedStringMap(secret.Object, "data")
	marker := base64.StdEncoding.EncodeToString([]byte(RedactionMarker))
	if data["password"] != marker {
		t.Error("password was not redacted")
	}
	if data["kmsVaultSecretIdForFilesystemEncryption"] != marker {
		t.Error("vault secret id was not redacted")
	}
	for key, want := range map[string]string{
		"username":     "admin",
		"organization": "Root",
		"endpoints":    "172.31.41.54:14000",
		"scheme":       "https",
	} {
		decoded, err := base64.StdEncoding.DecodeString(data[key])
		if err != nil || string(decoded) != want {
			t.Errorf("%s = %q, want it left intact as %q", key, decoded, want)
		}
	}
}

func TestRedactSecretSkipsEmptyValues(t *testing.T) {
	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "s", "namespace": "n"},
		"data":     map[string]any{"password": ""},
	}}
	redacted, err := RedactSecret(secret)
	if err != nil {
		t.Fatalf("RedactSecret returned error: %v", err)
	}
	if len(redacted) != 0 {
		t.Errorf("redacted %v, want nothing for an empty value", redacted)
	}
}

func TestRedactedKeysRoundTrip(t *testing.T) {
	secret := wekaSecret()
	if keys := RedactedKeys(secret); len(keys) != 0 {
		t.Errorf("unredacted secret reported %v", keys)
	}
	if _, err := RedactSecret(secret); err != nil {
		t.Fatalf("RedactSecret returned error: %v", err)
	}
	keys := RedactedKeys(secret)
	if len(keys) != 2 {
		t.Errorf("RedactedKeys = %v, want two entries", keys)
	}
}

func TestRedactSecretIsIdempotent(t *testing.T) {
	secret := wekaSecret()
	if _, err := RedactSecret(secret); err != nil {
		t.Fatalf("first RedactSecret returned error: %v", err)
	}
	again, err := RedactSecret(secret)
	if err != nil {
		t.Fatalf("second RedactSecret returned error: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("re-redacting reported %v, want nothing", again)
	}
}
