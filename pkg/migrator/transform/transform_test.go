package transform

import (
	"encoding/base64"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func pv() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolume",
		"metadata":   map[string]any{"name": "pv-dir"},
		"spec": map[string]any{
			"storageClassName": "sc-dir",
			"mountOptions":     []any{"noatime"},
			"claimRef": map[string]any{
				"apiVersion": "v1", "kind": "PersistentVolumeClaim",
				"namespace": "default", "name": "pvc-dir",
			},
			"csi": map[string]any{
				"driver": "csi.weka.io",
				// Doubled separator on purpose: it must survive a filesystem rename.
				"volumeHandle":         "weka/v2/testfs//csi-volumes/vol-abc",
				"volumeAttributes":     map[string]any{"filesystemName": "testfs"},
				"nodePublishSecretRef": map[string]any{"name": "api-secret", "namespace": "csi-wekafs"},
			},
			"nodeAffinity": map[string]any{
				"required": map[string]any{
					"nodeSelectorTerms": []any{map[string]any{
						"matchExpressions": []any{map[string]any{
							"key": "topology.weka-infra.weka.io/accessible", "operator": "In",
							"values": []any{"true"},
						}},
					}},
				},
			},
		},
	}}
}

func pvc() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata":   map[string]any{"name": "pvc-dir", "namespace": "default"},
		"spec": map[string]any{
			"storageClassName": "sc-dir",
			"volumeName":       "pv-dir",
		},
	}}
}

func storageClass() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion":  "storage.k8s.io/v1",
		"kind":        "StorageClass",
		"metadata":    map[string]any{"name": "sc-dir"},
		"provisioner": "csi.weka.io",
		"parameters": map[string]any{
			"filesystemName": "testfs",
			"csi.storage.k8s.io/provisioner-secret-name":      "api-secret",
			"csi.storage.k8s.io/provisioner-secret-namespace": "csi-wekafs",
		},
	}}
}

func secret() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": "api-secret", "namespace": "csi-wekafs"},
		"data": map[string]any{
			"username":  base64.StdEncoding.EncodeToString([]byte("admin")),
			"password":  base64.StdEncoding.EncodeToString([]byte("old-password")),
			"endpoints": base64.StdEncoding.EncodeToString([]byte("172.31.41.54:14000")),
		},
	}}
}

// applyAll runs a chain over a set of objects, as an import does.
func applyAll(t *testing.T, cfg *Config, objects ...*unstructured.Unstructured) *Chain {
	t.Helper()
	chain, err := NewChainFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewChainFromConfig returned error: %v", err)
	}
	for _, obj := range objects {
		if err := chain.Apply(obj); err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
	}
	return chain
}

func str(t *testing.T, u *unstructured.Unstructured, fields ...string) string {
	t.Helper()
	value, _, err := unstructured.NestedString(u.Object, fields...)
	if err != nil {
		t.Fatalf("reading %v: %v", fields, err)
	}
	return value
}

// TestNamespaceMappingMovesClaimAndClaimRef is the referential-integrity case: a claim that
// moves without its volume's claimRef following would never bind.
func TestNamespaceMappingMovesClaimAndClaimRef(t *testing.T) {
	volume, claim := pv(), pvc()
	applyAll(t, &Config{Namespaces: map[string]string{"default": "prod"}}, volume, claim)

	if claim.GetNamespace() != "prod" {
		t.Errorf("claim namespace = %q, want prod", claim.GetNamespace())
	}
	if got := str(t, volume, "spec", "claimRef", "namespace"); got != "prod" {
		t.Errorf("claimRef.namespace = %q, want prod", got)
	}
}

func TestTargetNamespaceCollapsesEverything(t *testing.T) {
	volume, claim := pv(), pvc()
	applyAll(t, &Config{TargetNamespace: "dr"}, volume, claim)

	if claim.GetNamespace() != "dr" {
		t.Errorf("claim namespace = %q, want dr", claim.GetNamespace())
	}
	if got := str(t, volume, "spec", "claimRef", "namespace"); got != "dr" {
		t.Errorf("claimRef.namespace = %q, want dr", got)
	}
}

// TestFilesystemRenameRewritesHandleAndParameterTogether is the lockstep requirement: the
// handle and the StorageClass parameter must never disagree about where the data lives.
func TestFilesystemRenameRewritesHandleAndParameterTogether(t *testing.T) {
	volume, class := pv(), storageClass()
	applyAll(t, &Config{Filesystems: map[string]string{"testfs": "replicated-fs"}}, volume, class)

	// The handle must be spliced, preserving the doubled separator byte-for-byte.
	if got := str(t, volume, "spec", "csi", "volumeHandle"); got != "weka/v2/replicated-fs//csi-volumes/vol-abc" {
		t.Errorf("volumeHandle = %q, want the filesystem replaced and everything else intact", got)
	}
	if got := str(t, volume, "spec", "csi", "volumeAttributes", "filesystemName"); got != "replicated-fs" {
		t.Errorf("volumeAttributes.filesystemName = %q, want replicated-fs: the object contradicts its own handle", got)
	}
	if got := str(t, class, "parameters", "filesystemName"); got != "replicated-fs" {
		t.Errorf("StorageClass filesystemName = %q, want replicated-fs", got)
	}
}

func TestFilesystemRenameLeavesUnknownHandlesAlone(t *testing.T) {
	volume := pv()
	_ = unstructured.SetNestedField(volume.Object, "not-a-weka-handle", "spec", "csi", "volumeHandle")
	applyAll(t, &Config{Filesystems: map[string]string{"testfs": "replicated-fs"}}, volume)

	if got := str(t, volume, "spec", "csi", "volumeHandle"); got != "not-a-weka-handle" {
		t.Errorf("an unparseable handle was rewritten to %q", got)
	}
}

func TestStorageClassRenameMovesClassVolumeAndClaim(t *testing.T) {
	volume, claim, class := pv(), pvc(), storageClass()
	applyAll(t, &Config{StorageClasses: map[string]string{"sc-dir": "sc-dr"}}, volume, claim, class)

	if class.GetName() != "sc-dr" {
		t.Errorf("StorageClass name = %q, want sc-dr", class.GetName())
	}
	if got := str(t, volume, "spec", "storageClassName"); got != "sc-dr" {
		t.Errorf("PV storageClassName = %q, want sc-dr", got)
	}
	if got := str(t, claim, "spec", "storageClassName"); got != "sc-dr" {
		t.Errorf("PVC storageClassName = %q, want sc-dr: the claim would not match its volume", got)
	}
}

// TestPVRenameUpdatesClaimVolumeName covers the binding that makes a claim static.
func TestPVRenameUpdatesClaimVolumeName(t *testing.T) {
	volume, claim := pv(), pvc()
	applyAll(t, &Config{PersistentVolumes: map[string]string{"pv-dir": "pv-dr"}}, volume, claim)

	if volume.GetName() != "pv-dr" {
		t.Errorf("PV name = %q, want pv-dr", volume.GetName())
	}
	if got := str(t, claim, "spec", "volumeName"); got != "pv-dr" {
		t.Errorf("PVC volumeName = %q, want pv-dr: the claim would wait forever", got)
	}
}

func TestPVCRenameUpdatesClaimRef(t *testing.T) {
	volume, claim := pv(), pvc()
	applyAll(t, &Config{PersistentVolumeClaims: map[string]string{"default/pvc-dir": "pvc-dr"}}, volume, claim)

	if claim.GetName() != "pvc-dr" {
		t.Errorf("PVC name = %q, want pvc-dr", claim.GetName())
	}
	if got := str(t, volume, "spec", "claimRef", "name"); got != "pvc-dr" {
		t.Errorf("claimRef.name = %q, want pvc-dr", got)
	}
}

// TestRulesAreOrderIndependent is the property the immutable-snapshot design exists for.
// A namespace mapping and a claim rename both touch claimRef; if rules keyed on the live
// object, whichever ran second would fail to match.
func TestRulesAreOrderIndependent(t *testing.T) {
	volume, claim := pv(), pvc()
	applyAll(t, &Config{
		Namespaces:             map[string]string{"default": "prod"},
		PersistentVolumeClaims: map[string]string{"default/pvc-dir": "pvc-dr"},
	}, volume, claim)

	if claim.GetNamespace() != "prod" || claim.GetName() != "pvc-dr" {
		t.Errorf("claim = %s/%s, want prod/pvc-dr", claim.GetNamespace(), claim.GetName())
	}
	namespace := str(t, volume, "spec", "claimRef", "namespace")
	name := str(t, volume, "spec", "claimRef", "name")
	if namespace != "prod" || name != "pvc-dr" {
		t.Errorf("claimRef = %s/%s, want prod/pvc-dr: rules are keying on mutated state", namespace, name)
	}
}

// TestSecretOverrideRewritesSecretAndEveryReference covers scenario (d): a different Weka
// cluster in another network, reached with different credentials from a different namespace.
func TestSecretOverrideRewritesSecretAndEveryReference(t *testing.T) {
	volume, class, sec := pv(), storageClass(), secret()
	applyAll(t, &Config{Secrets: map[string]SecretOverride{
		"csi-wekafs/api-secret": {
			Name:      "weka-dr-api",
			Namespace: "weka-dr",
			Data: map[string]string{
				"endpoints": "10.20.30.40:14000",
				"password":  "dr-password",
			},
		},
	}}, volume, class, sec)

	if sec.GetName() != "weka-dr-api" || sec.GetNamespace() != "weka-dr" {
		t.Errorf("secret = %s/%s, want weka-dr/weka-dr-api", sec.GetNamespace(), sec.GetName())
	}

	data, _, _ := unstructured.NestedStringMap(sec.Object, "data")
	for key, want := range map[string]string{
		"endpoints": "10.20.30.40:14000",
		"password":  "dr-password",
		"username":  "admin", // untouched
	} {
		decoded, err := base64.StdEncoding.DecodeString(data[key])
		if err != nil {
			t.Fatalf("data[%s] is not base64: %v", key, err)
		}
		if string(decoded) != want {
			t.Errorf("data[%s] = %q, want %q", key, decoded, want)
		}
	}

	// Every reference must follow, or the driver looks for a secret that is not there.
	if got := str(t, volume, "spec", "csi", "nodePublishSecretRef", "name"); got != "weka-dr-api" {
		t.Errorf("PV secretRef name = %q, want weka-dr-api", got)
	}
	if got := str(t, volume, "spec", "csi", "nodePublishSecretRef", "namespace"); got != "weka-dr" {
		t.Errorf("PV secretRef namespace = %q, want weka-dr", got)
	}
	if got := str(t, class, "parameters", "csi.storage.k8s.io/provisioner-secret-name"); got != "weka-dr-api" {
		t.Errorf("SC secret name param = %q, want weka-dr-api", got)
	}
	if got := str(t, class, "parameters", "csi.storage.k8s.io/provisioner-secret-namespace"); got != "weka-dr" {
		t.Errorf("SC secret namespace param = %q, want weka-dr", got)
	}
}

func TestSecretDataFromEnvironment(t *testing.T) {
	t.Setenv("WEKA_DR_PASSWORD", "from-env")
	sec := secret()
	applyAll(t, &Config{Secrets: map[string]SecretOverride{
		"csi-wekafs/api-secret": {Data: map[string]string{"password": "${WEKA_DR_PASSWORD}"}},
	}}, sec)

	data, _, _ := unstructured.NestedStringMap(sec.Object, "data")
	decoded, _ := base64.StdEncoding.DecodeString(data["password"])
	if string(decoded) != "from-env" {
		t.Errorf("password = %q, want from-env", decoded)
	}
}

// TestUnsetEnvironmentVariableIsAnError guards against silently writing an empty credential,
// which would fail at first mount with nothing pointing at the cause.
func TestUnsetEnvironmentVariableIsAnError(t *testing.T) {
	chain, err := NewChainFromConfig(&Config{Secrets: map[string]SecretOverride{
		"csi-wekafs/api-secret": {Data: map[string]string{"password": "${DEFINITELY_NOT_SET_12345}"}},
	}})
	if err != nil {
		t.Fatalf("NewChainFromConfig returned error: %v", err)
	}
	err = chain.Apply(secret())
	if err == nil {
		t.Fatal("an unset environment variable was accepted")
	}
	if !strings.Contains(err.Error(), "DEFINITELY_NOT_SET_12345") {
		t.Errorf("error does not name the missing variable: %v", err)
	}
}

func TestSecretRemoveData(t *testing.T) {
	sec := secret()
	applyAll(t, &Config{Secrets: map[string]SecretOverride{
		"csi-wekafs/api-secret": {RemoveData: []string{"endpoints"}},
	}}, sec)

	data, _, _ := unstructured.NestedStringMap(sec.Object, "data")
	if _, present := data["endpoints"]; present {
		t.Error("endpoints was not removed")
	}
	if _, present := data["username"]; !present {
		t.Error("removeData deleted more than it was asked to")
	}
}

func TestMountOptionsSingleValueAppliesToEveryVolume(t *testing.T) {
	var spec MountOptionsSpec
	if err := spec.UnmarshalJSON([]byte(`"ro,noatime"`)); err != nil {
		t.Fatalf("unmarshal returned error: %v", err)
	}
	volume := pv()
	applyAll(t, &Config{MountOptions: &spec}, volume)

	options, _, _ := unstructured.NestedStringSlice(volume.Object, "spec", "mountOptions")
	if len(options) != 2 || options[0] != "ro" || options[1] != "noatime" {
		t.Errorf("mountOptions = %v, want [ro noatime]", options)
	}
}

func TestMountOptionsPerVolume(t *testing.T) {
	var spec MountOptionsSpec
	if err := spec.UnmarshalJSON([]byte(`{"pv-dir":["ro"],"pv-other":["rw"]}`)); err != nil {
		t.Fatalf("unmarshal returned error: %v", err)
	}
	volume := pv()
	applyAll(t, &Config{MountOptions: &spec}, volume)

	options, _, _ := unstructured.NestedStringSlice(volume.Object, "spec", "mountOptions")
	if len(options) != 1 || options[0] != "ro" {
		t.Errorf("mountOptions = %v, want [ro]", options)
	}
}

// TestMountOptionsEmptyListClears distinguishes "not configured" from "configured empty".
func TestMountOptionsEmptyListClears(t *testing.T) {
	var spec MountOptionsSpec
	if err := spec.UnmarshalJSON([]byte(`[]`)); err != nil {
		t.Fatalf("unmarshal returned error: %v", err)
	}
	volume := pv()
	applyAll(t, &Config{MountOptions: &spec}, volume)

	if _, found, _ := unstructured.NestedFieldNoCopy(volume.Object, "spec", "mountOptions"); found {
		t.Error("an explicit empty list did not clear mountOptions")
	}
}

// TestNodeAffinityReplacesKeyNotOnlyValues covers a target cluster whose driver publishes a
// different topology key entirely.
func TestNodeAffinityReplacesKeyNotOnlyValues(t *testing.T) {
	volume := pv()
	applyAll(t, &Config{NodeAffinity: &NodeAffinitySpec{
		Key:    "topology.weka-dr.weka.io/accessible",
		Values: []string{"true"},
	}}, volume)

	terms, found, err := unstructured.NestedSlice(volume.Object, "spec", "nodeAffinity", "required", "nodeSelectorTerms")
	if err != nil || !found || len(terms) != 1 {
		t.Fatalf("nodeSelectorTerms = %v (found=%v, err=%v)", terms, found, err)
	}
	expressions := terms[0].(map[string]any)["matchExpressions"].([]any)
	expression := expressions[0].(map[string]any)
	if expression["key"] != "topology.weka-dr.weka.io/accessible" {
		t.Errorf("key = %v, want the DR topology key", expression["key"])
	}
	if expression["operator"] != "In" {
		t.Errorf("operator = %v, want the In default", expression["operator"])
	}
}

func TestNodeAffinityRemove(t *testing.T) {
	volume := pv()
	applyAll(t, &Config{NodeAffinity: &NodeAffinitySpec{Remove: true}}, volume)

	if _, found, _ := unstructured.NestedFieldNoCopy(volume.Object, "spec", "nodeAffinity"); found {
		t.Error("nodeAffinity was not removed")
	}
}

func TestMetadataSetRemoveRename(t *testing.T) {
	volume := pv()
	volume.SetAnnotations(map[string]string{"old.example.com/team": "storage", "drop.me": "yes"})
	applyAll(t, &Config{Metadata: &MetadataSpec{
		Annotations: &MapEdit{
			Set:    map[string]string{"migrated-from": "prod-us-east"},
			Remove: []string{"drop.me"},
			Rename: map[string]string{"old.example.com/team": "new.example.com/team"},
		},
		Labels: &MapEdit{Set: map[string]string{"tier": "dr"}},
	}}, volume)

	annotations := volume.GetAnnotations()
	if annotations["migrated-from"] != "prod-us-east" {
		t.Errorf("set did not apply: %v", annotations)
	}
	if _, present := annotations["drop.me"]; present {
		t.Error("remove did not apply")
	}
	if annotations["new.example.com/team"] != "storage" {
		t.Errorf("rename did not carry the value across: %v", annotations)
	}
	if _, present := annotations["old.example.com/team"]; present {
		t.Error("rename left the old key behind")
	}
	if volume.GetLabels()["tier"] != "dr" {
		t.Errorf("labels = %v, want tier=dr", volume.GetLabels())
	}
}

func TestMetadataKindsFilter(t *testing.T) {
	volume, claim := pv(), pvc()
	applyAll(t, &Config{Metadata: &MetadataSpec{
		Kinds:       []string{"PersistentVolume"},
		Annotations: &MapEdit{Set: map[string]string{"only": "volumes"}},
	}}, volume, claim)

	if volume.GetAnnotations()["only"] != "volumes" {
		t.Error("the filtered kind was not annotated")
	}
	if _, present := claim.GetAnnotations()["only"]; present {
		t.Error("an unfiltered kind was annotated")
	}
}

// TestUnusedMappingsAreReported catches the typo that would otherwise be discovered only
// when a pod fails to mount.
func TestUnusedMappingsAreReported(t *testing.T) {
	chain := applyAll(t, &Config{
		Filesystems:       map[string]string{"testfs": "replicated-fs", "typo-fs": "nope"},
		PersistentVolumes: map[string]string{"pv-dir": "pv-dr"},
	}, pv())

	unused := chain.UnusedMappings()
	if len(unused) != 1 || !strings.Contains(unused[0], "typo-fs") {
		t.Errorf("UnusedMappings() = %v, want only the typo-fs entry", unused)
	}
}

func TestChangesAreRecordedWithoutLeakingCredentials(t *testing.T) {
	chain := applyAll(t, &Config{
		Secrets: map[string]SecretOverride{
			"csi-wekafs/api-secret": {Data: map[string]string{"password": "top-secret-value"}},
		},
	}, secret())

	if len(chain.Changes()) == 0 {
		t.Fatal("no changes were recorded")
	}
	for _, change := range chain.Changes() {
		if strings.Contains(change.String(), "top-secret-value") {
			t.Errorf("a credential leaked into the change log: %s", change)
		}
	}
}

func TestIdentityChainChangesNothing(t *testing.T) {
	volume := pv()
	before, err := volume.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	chain := NewChain()
	if err := chain.Apply(volume); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	after, _ := volume.MarshalJSON()
	if string(before) != string(after) {
		t.Error("the identity chain modified an object")
	}
}

// TestDriverNameRetargetsVolumesAndClasses covers a target cluster running the driver under
// a different name, which the chart's csiDriverName makes perfectly possible.
func TestDriverNameRetargetsVolumesAndClasses(t *testing.T) {
	volume, class := pv(), storageClass()
	applyAll(t, &Config{DriverName: "weka-infra.weka.io"}, volume, class)

	if got := str(t, volume, "spec", "csi", "driver"); got != "weka-infra.weka.io" {
		t.Errorf("PV driver = %q, want weka-infra.weka.io: no node could stage this volume", got)
	}
	if got := str(t, class, "provisioner"); got != "weka-infra.weka.io" {
		t.Errorf("StorageClass provisioner = %q, want weka-infra.weka.io", got)
	}
}

// TestDriverNameLeavesClaimsAlone confirms the rule touches only the two fields that carry
// the driver name; a claim references its driver only indirectly, through its class.
func TestDriverNameLeavesClaimsAlone(t *testing.T) {
	claim := pvc()
	before, err := claim.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	applyAll(t, &Config{DriverName: "weka-infra.weka.io"}, claim)
	after, _ := claim.MarshalJSON()
	if string(before) != string(after) {
		t.Error("the driver rule modified a PersistentVolumeClaim")
	}
}

func TestDriverNameMatchingSourceRecordsNoChange(t *testing.T) {
	chain := applyAll(t, &Config{DriverName: "csi.weka.io"}, pv(), storageClass())
	if len(chain.Changes()) != 0 {
		t.Errorf("retargeting to the same driver recorded %v", chain.Changes())
	}
	// It still counts as used: the archive did contain driver-bearing objects.
	if unused := chain.UnusedMappings(); len(unused) != 0 {
		t.Errorf("UnusedMappings() = %v, want none", unused)
	}
}

// TestDriverNameUnusedWhenNothingCarriesIt catches a mapping file aimed at an archive with
// no volumes or classes in it.
func TestDriverNameUnusedWhenNothingCarriesIt(t *testing.T) {
	chain := applyAll(t, &Config{DriverName: "weka-infra.weka.io"}, secret())
	unused := chain.UnusedMappings()
	if len(unused) != 1 || unused[0] != "driverName" {
		t.Errorf("UnusedMappings() = %v, want [driverName]", unused)
	}
}

// TestDriverNameComposesWithStorageClassRename guards the interaction between two rules that
// both touch a StorageClass: renaming the class and retargeting its provisioner.
func TestDriverNameComposesWithStorageClassRename(t *testing.T) {
	class := storageClass()
	applyAll(t, &Config{
		DriverName:     "weka-infra.weka.io",
		StorageClasses: map[string]string{"sc-dir": "sc-dr"},
	}, class)

	if class.GetName() != "sc-dr" {
		t.Errorf("class name = %q, want sc-dr", class.GetName())
	}
	if got := str(t, class, "provisioner"); got != "weka-infra.weka.io" {
		t.Errorf("provisioner = %q, want weka-infra.weka.io", got)
	}
}
