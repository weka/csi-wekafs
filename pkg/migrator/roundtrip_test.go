// Package migrator_test exercises export and import together against fake clusters.
//
// These tests are the ones that would catch a regression an operator would actually feel:
// a volume that comes back pointing at the wrong data, a claim that provisions fresh empty
// storage instead of rebinding, or credentials leaking into an unencrypted archive.
package migrator_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/wekafs/csi-wekafs/pkg/migrator/apply"
	"github.com/wekafs/csi-wekafs/pkg/migrator/archive"
	"github.com/wekafs/csi-wekafs/pkg/migrator/collect"
	"github.com/wekafs/csi-wekafs/pkg/migrator/convert"
	"github.com/wekafs/csi-wekafs/pkg/migrator/transform"
)

const (
	driverName = "csi.weka.io"
	// dirHandle deliberately carries the doubled separator produced when dynamicVolPath is
	// empty, so the round trip proves handles survive byte-for-byte.
	dirHandle  = "weka/v2/testfs//csi-volumes/vol-abc"
	fsHandle   = "weka/v2/csivol-fsvol-97ab4a2a2b6d"
	snapHandle = "weka/v2/testfs:snapvol-12ab34cd56ef"
)

func strPtr(s string) *string { return &s }

func secret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: "secret-uid", ResourceVersion: "10"},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"username":     []byte("admin"),
			"password":     []byte("s3cr3t-password"),
			"organization": []byte("Root"),
			"endpoints":    []byte("172.31.41.54:14000"),
			"scheme":       []byte("https"),
		},
	}
}

func storageClass(name, filesystemName string) *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: name, UID: "sc-uid", ResourceVersion: "11"},
		Provisioner: driverName,
		Parameters: map[string]string{
			"filesystemName": filesystemName,
			"volumeType":     "weka/v2",
			"csi.storage.k8s.io/provisioner-secret-name":       "csi-wekafs-api-secret",
			"csi.storage.k8s.io/provisioner-secret-namespace":  "csi-wekafs",
			"csi.storage.k8s.io/node-publish-secret-name":      "csi-wekafs-api-secret",
			"csi.storage.k8s.io/node-publish-secret-namespace": "csi-wekafs",
		},
	}
}

func dynamicPV(name, handle, className, claimNamespace, claimName string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			UID:             types.UID("pv-uid-" + name),
			ResourceVersion: "12",
			Finalizers:      []string{"kubernetes.io/pv-protection"},
			Annotations: map[string]string{
				convert.AnnProvisionedBy:                                driverName,
				"volume.kubernetes.io/provisioner-deletion-secret-name": "csi-wekafs-api-secret",
			},
		},
		Spec: corev1.PersistentVolumeSpec{
			Capacity:                      corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			StorageClassName:              className,
			ClaimRef: &corev1.ObjectReference{
				APIVersion: "v1", Kind: "PersistentVolumeClaim",
				Namespace: claimNamespace, Name: claimName,
				UID: types.UID("claim-uid-" + claimName), ResourceVersion: "13",
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       driverName,
					VolumeHandle: handle,
					VolumeAttributes: map[string]string{
						convert.AttrProvisionerIdentity: "1754380800000-8081-csi.weka.io",
						"filesystemName":                "testfs",
					},
					NodePublishSecretRef:      &corev1.SecretReference{Namespace: "csi-wekafs", Name: "csi-wekafs-api-secret"},
					ControllerExpandSecretRef: &corev1.SecretReference{Namespace: "csi-wekafs", Name: "csi-wekafs-api-secret"},
				},
			},
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}
}

func boundPVC(namespace, name, className, pvName string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace, Name: name,
			UID: types.UID("claim-uid-" + name), ResourceVersion: "14",
			Finalizers: []string{"kubernetes.io/pvc-protection"},
			Annotations: map[string]string{
				"pv.kubernetes.io/bind-completed":          "yes",
				"volume.kubernetes.io/storage-provisioner": driverName,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			StorageClassName: strPtr(className),
			VolumeName:       pvName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
}

// sourceCluster builds a cluster holding a directory-backed volume, a filesystem-backed
// volume, a snapshot-backed volume, and one foreign volume from another CSI driver.
func sourceCluster() kubernetes.Interface {
	foreign := dynamicPV("pv-foreign", "some/handle", "other-sc", "default", "pvc-foreign")
	foreign.Spec.CSI.Driver = "ebs.csi.aws.com"

	return fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", UID: "cluster-uid-1"}},
		secret("csi-wekafs", "csi-wekafs-api-secret"),
		storageClass("sc-dir", "testfs"),
		storageClass("sc-fs", ""),
		dynamicPV("pv-dir", dirHandle, "sc-dir", "default", "pvc-dir"),
		boundPVC("default", "pvc-dir", "sc-dir", "pv-dir"),
		dynamicPV("pv-fs", fsHandle, "sc-fs", "team-a", "pvc-fs"),
		boundPVC("team-a", "pvc-fs", "sc-fs", "pv-fs"),
		dynamicPV("pv-snap", snapHandle, "sc-dir", "default", "pvc-snap"),
		boundPVC("default", "pvc-snap", "sc-dir", "pv-snap"),
		foreign,
		boundPVC("default", "pvc-foreign", "other-sc", "pv-foreign"),
	)
}

func exportTo(t *testing.T, client kubernetes.Interface, opts collect.Options, password string) []byte {
	t.Helper()
	if opts.DriverName == "" {
		opts.DriverName = driverName
	}
	opts.Tool = "weka-csi-migrator/test"

	writer, err := collect.New(client, opts).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	var buf bytes.Buffer
	if err := writer.WriteTo(&buf, password); err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}
	return buf.Bytes()
}

func openArchive(t *testing.T, raw []byte, password string) *archive.Reader {
	t.Helper()
	reader, _, err := archive.Open(bytes.NewReader(raw), archive.OpenOptions{Password: password})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	return reader
}

// TestRoundTripRestoresVolumesToAnEmptyCluster is scenario (b): the Kubernetes cluster was
// lost, the Weka cluster survived, and the export is restored onto a fresh cluster.
func TestRoundTripRestoresVolumesToAnEmptyCluster(t *testing.T) {
	raw := exportTo(t, sourceCluster(), collect.Options{IncludeSecretData: true}, "pw")
	reader := openArchive(t, raw, "pw")

	target := fake.NewSimpleClientset()
	results, err := apply.New(target, apply.Options{}).Apply(context.Background(), reader)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("nothing was applied")
	}

	ctx := context.Background()
	pv, err := target.CoreV1().PersistentVolumes().Get(ctx, "pv-dir", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("restored PV missing: %v", err)
	}

	// The single most important assertion in the suite: the handle locates the data.
	if pv.Spec.CSI.VolumeHandle != dirHandle {
		t.Errorf("volumeHandle = %q, want %q", pv.Spec.CSI.VolumeHandle, dirHandle)
	}
	if pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.UID != "" {
		t.Errorf("claimRef = %+v, want the pairing kept but the uid cleared", pv.Spec.ClaimRef)
	}
	if pv.Spec.ClaimRef.Name != "pvc-dir" || pv.Spec.ClaimRef.Namespace != "default" {
		t.Errorf("claimRef identity lost: %s/%s", pv.Spec.ClaimRef.Namespace, pv.Spec.ClaimRef.Name)
	}
	if _, ok := pv.Annotations[convert.AnnProvisionedBy]; ok {
		t.Error("restored PV is still marked as dynamically provisioned")
	}
	if _, ok := pv.Spec.CSI.VolumeAttributes[convert.AttrProvisionerIdentity]; ok {
		t.Error("restored PV carries the source provisioner identity")
	}
	if pv.Spec.CSI.VolumeAttributes["filesystemName"] != "testfs" {
		t.Error("driver-meaningful volume attributes were lost")
	}

	claim, err := target.CoreV1().PersistentVolumeClaims("default").Get(ctx, "pvc-dir", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("restored PVC missing: %v", err)
	}
	if claim.Spec.VolumeName != "pv-dir" {
		t.Errorf("spec.volumeName = %q, want pv-dir: the claim would provision new empty storage", claim.Spec.VolumeName)
	}

	if _, err := target.StorageV1().StorageClasses().Get(ctx, "sc-dir", metav1.GetOptions{}); err != nil {
		t.Errorf("StorageClass was not restored: %v", err)
	}
	restoredSecret, err := target.CoreV1().Secrets("csi-wekafs").Get(ctx, "csi-wekafs-api-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Secret was not restored: %v", err)
	}
	if string(restoredSecret.Data["password"]) != "s3cr3t-password" {
		t.Error("credentials did not survive an --include-secret-data round trip")
	}
}

// TestForeignDriverVolumesAreNotExported keeps the tool from claiming volumes belonging to
// another CSI driver.
func TestForeignDriverVolumesAreNotExported(t *testing.T) {
	reader := openArchive(t, exportTo(t, sourceCluster(), collect.Options{IncludeSecretData: true}, ""), "")
	for _, entry := range reader.Entries() {
		if strings.Contains(entry.Path, "foreign") {
			t.Errorf("archive contains %q from another CSI driver", entry.Path)
		}
	}
}

// TestSnapshotBackedVolumesAreWarnedAbout covers the Weka limitation: snapshots are not
// replicated, so those volumes can only be restored against the same Weka cluster.
func TestSnapshotBackedVolumesAreWarnedAbout(t *testing.T) {
	reader := openArchive(t, exportTo(t, sourceCluster(), collect.Options{IncludeSecretData: true}, ""), "")

	var snapRecord *archive.VolumeRecord
	for i, v := range reader.Manifest.Volumes {
		if v.PVName == "pv-snap" {
			snapRecord = &reader.Manifest.Volumes[i]
		}
	}
	if snapRecord == nil {
		t.Fatal("snapshot-backed volume was not exported")
	}
	if snapRecord.PortableAcrossWekaClusters {
		t.Error("snapshot-backed volume is reported as portable across Weka clusters")
	}
	if snapRecord.Backing != "snapshot" {
		t.Errorf("backing = %q, want snapshot", snapRecord.Backing)
	}

	var warned bool
	for _, w := range reader.Manifest.Warnings {
		if strings.Contains(w, "pv-snap") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning recorded for the snapshot-backed volume: %v", reader.Manifest.Warnings)
	}
}

func TestSkipUnexportableDropsSnapshotVolumes(t *testing.T) {
	reader := openArchive(t, exportTo(t, sourceCluster(),
		collect.Options{IncludeSecretData: true, SkipUnexportable: true}, ""), "")

	for _, v := range reader.Manifest.Volumes {
		if v.PVName == "pv-snap" {
			t.Error("snapshot-backed volume survived --skip-unexportable")
		}
	}
	// The portable volumes must still be there.
	if len(reader.Manifest.Volumes) != 2 {
		t.Errorf("exported %d volumes, want the 2 portable ones", len(reader.Manifest.Volumes))
	}
	for _, entry := range reader.Entries() {
		if strings.Contains(entry.Path, "pvc-snap") {
			t.Errorf("claim %q of a skipped volume was still exported", entry.Path)
		}
	}
}

// TestNamespaceScopedExport covers the per-namespace export, including the requirement that
// secrets living elsewhere are still followed.
func TestNamespaceScopedExport(t *testing.T) {
	reader := openArchive(t, exportTo(t, sourceCluster(),
		collect.Options{Namespace: "team-a", IncludeSecretData: true}, ""), "")

	if len(reader.Manifest.Volumes) != 1 || reader.Manifest.Volumes[0].PVName != "pv-fs" {
		t.Fatalf("exported %+v, want only the team-a volume", reader.Manifest.Volumes)
	}
	var sawSecret bool
	for _, entry := range reader.Entries() {
		if strings.Contains(entry.Path, "default/") {
			t.Errorf("archive contains %q from outside the requested namespace", entry.Path)
		}
		if entry.Kind == "Secret" && entry.Namespace == "csi-wekafs" {
			sawSecret = true
		}
	}
	if !sawSecret {
		t.Error("the API secret in csi-wekafs was not followed out of the requested namespace")
	}
}

// TestRedactedExportIsRefusedOnImport is the guard against restoring a cluster whose driver
// then cannot authenticate, with an error that points nowhere near the cause.
func TestRedactedExportIsRefusedOnImport(t *testing.T) {
	reader := openArchive(t, exportTo(t, sourceCluster(), collect.Options{}, ""), "")

	_, err := apply.New(fake.NewSimpleClientset(), apply.Options{}).Apply(context.Background(), reader)
	if err == nil {
		t.Fatal("a redacted archive was imported without complaint")
	}
	if !strings.Contains(err.Error(), "--include-secret-data") {
		t.Errorf("error does not explain the fix: %v", err)
	}

	// With the override it must proceed, since the operator has taken responsibility.
	if _, err := apply.New(fake.NewSimpleClientset(), apply.Options{AllowRedactedSecrets: true}).
		Apply(context.Background(), reader); err != nil {
		t.Errorf("--allow-redacted-secrets did not permit the import: %v", err)
	}
}

// TestRedactedExportKeepsMigrationRelevantFields is what makes a redacted archive useful for
// planning a move to another Weka cluster.
func TestRedactedExportKeepsMigrationRelevantFields(t *testing.T) {
	reader := openArchive(t, exportTo(t, sourceCluster(), collect.Options{}, ""), "")

	body, ok := reader.Body("objects/secrets/csi-wekafs/csi-wekafs-api-secret.yaml")
	if !ok {
		t.Fatal("secret missing from archive")
	}
	if bytes.Contains(body, []byte(base64.StdEncoding.EncodeToString([]byte("s3cr3t-password")))) {
		t.Error("the password leaked into a redacted export")
	}
	for _, field := range []string{"172.31.41.54:14000", "admin", "Root", "https"} {
		if !bytes.Contains(body, []byte(base64.StdEncoding.EncodeToString([]byte(field)))) {
			t.Errorf("%q was redacted, but it is needed to plan a migration", field)
		}
	}
}

// TestImportRefusesToOverwrite protects a live cluster from an accidental import.
func TestImportRefusesToOverwrite(t *testing.T) {
	reader := openArchive(t, exportTo(t, sourceCluster(), collect.Options{IncludeSecretData: true}, ""), "")

	occupied := fake.NewSimpleClientset(dynamicPV("pv-dir", "weka/v2/somethingelse", "sc-dir", "default", "pvc-dir"))
	_, err := apply.New(occupied, apply.Options{}).Apply(context.Background(), reader)
	if err == nil {
		t.Fatal("import overwrote or ignored an existing PersistentVolume")
	}
	if !strings.Contains(err.Error(), "--skip-existing") {
		t.Errorf("error does not mention the way forward: %v", err)
	}

	results, err := apply.New(occupied, apply.Options{SkipExisting: true}).Apply(context.Background(), reader)
	if err != nil {
		t.Fatalf("--skip-existing did not allow the import: %v", err)
	}
	var skipped bool
	for _, r := range results {
		if r.Name == "pv-dir" && r.Action == "exists" {
			skipped = true
		}
	}
	if !skipped {
		t.Error("the pre-existing volume was not reported as skipped")
	}

	// The existing object must be untouched.
	pv, err := occupied.CoreV1().PersistentVolumes().Get(context.Background(), "pv-dir", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting pv-dir: %v", err)
	}
	if pv.Spec.CSI.VolumeHandle != "weka/v2/somethingelse" {
		t.Error("--skip-existing modified the existing volume")
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	reader := openArchive(t, exportTo(t, sourceCluster(), collect.Options{IncludeSecretData: true}, ""), "")

	target := fake.NewSimpleClientset()
	results, err := apply.New(target, apply.Options{DryRun: true}).Apply(context.Background(), reader)
	if err != nil {
		t.Fatalf("dry run returned error: %v", err)
	}
	for _, r := range results {
		if r.Action != "would create" {
			t.Errorf("dry run reported %q for %s", r.Action, r.Name)
		}
	}
	list, err := target.CoreV1().PersistentVolumes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing PVs: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("dry run created %d PersistentVolume(s)", len(list.Items))
	}
}

// TestApplyOrderCreatesDependenciesFirst pins the ordering that keeps a restored claim from
// provisioning fresh storage before its volume exists.
func TestApplyOrderCreatesDependenciesFirst(t *testing.T) {
	reader := openArchive(t, exportTo(t, sourceCluster(), collect.Options{IncludeSecretData: true}, ""), "")

	results, err := apply.New(fake.NewSimpleClientset(), apply.Options{}).Apply(context.Background(), reader)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	position := map[string]int{}
	for i, r := range results {
		if _, seen := position[r.Kind]; !seen {
			position[r.Kind] = i
		}
	}
	for _, pair := range [][2]string{
		{"Secret", "StorageClass"},
		{"StorageClass", "PersistentVolume"},
		{"PersistentVolume", "PersistentVolumeClaim"},
	} {
		if position[pair[0]] > position[pair[1]] {
			t.Errorf("%s was applied after %s", pair[0], pair[1])
		}
	}
}

// recordingRule counts the objects passed through it and renames claims, so the test can
// prove both that the chain runs and that its effects reach the cluster.
type recordingRule struct{ seen *[]string }

func (r recordingRule) Name() string { return "recording" }

func (r recordingRule) Apply(obj, _ *unstructured.Unstructured) error {
	*r.seen = append(*r.seen, obj.GetKind()+"/"+obj.GetName())
	if obj.GetKind() == "PersistentVolumeClaim" {
		obj.SetName(obj.GetName() + "-renamed")
	}
	return nil
}

// TestTransformChainRunsOnImport guards the phase-2 seam. The chain is empty in v1, so
// nothing here would fail if it were quietly unplumbed — which is exactly why it needs a
// test rather than a comment claiming the import path uses it.
func TestTransformChainRunsOnImport(t *testing.T) {
	reader := openArchive(t, exportTo(t, sourceCluster(), collect.Options{IncludeSecretData: true}, ""), "")

	var seen []string
	target := fake.NewSimpleClientset()
	_, err := apply.New(target, apply.Options{
		Transform: transform.NewChain(recordingRule{seen: &seen}),
	}).Apply(context.Background(), reader)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	if len(seen) == 0 {
		t.Fatal("the transform chain was never invoked: the seam is decorative")
	}
	// Every applied object must pass through, not just some kinds.
	if len(seen) != len(reader.Entries()) {
		t.Errorf("chain saw %d objects, want all %d", len(seen), len(reader.Entries()))
	}

	// A rename must reach the cluster, proving the chain runs before creation rather than
	// against a throwaway copy.
	if _, err := target.CoreV1().PersistentVolumeClaims("default").
		Get(context.Background(), "pvc-dir-renamed", metav1.GetOptions{}); err != nil {
		t.Errorf("transformed claim was not created under its new name: %v", err)
	}
}

// TestScenarioDCrossGeographyMigration is the whole point of phase 2: restore onto a
// different Kubernetes cluster backed by a *different* Weka cluster in another geography,
// where the filesystem was renamed by replication, the namespaces differ, and the API
// endpoint and credentials are new.
func TestScenarioDCrossGeographyMigration(t *testing.T) {
	t.Setenv("WEKA_DR_PASSWORD", "dr-password")

	// Redacted export: the DR site has its own credentials, so there is no reason to carry
	// the production ones across.
	reader := openArchive(t, exportTo(t, sourceCluster(),
		collect.Options{SkipUnexportable: true}, ""), "")

	cfg, err := transform.ParseConfig([]byte(`
namespaces:
  default: dr-default
  team-a: dr-team-a
filesystems:
  testfs: testfs-replica
storageClasses:
  sc-dir: sc-dir-dr
secrets:
  csi-wekafs/csi-wekafs-api-secret:
    namespace: weka-dr
    data:
      endpoints: 10.20.30.40:14000
      organization: DR
      password: ${WEKA_DR_PASSWORD}
nodeAffinity:
  key: topology.weka-dr.weka.io/accessible
  values: ["true"]
metadata:
  annotations:
    set:
      migrated-from: prod-us-east
`))
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	chain, err := transform.NewChainFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewChainFromConfig returned error: %v", err)
	}

	target := fake.NewSimpleClientset()
	// No --allow-redacted-secrets: the transform supplies the credentials, which is the
	// normal shape of a cross-cluster move.
	if _, err := apply.New(target, apply.Options{Transform: chain}).Apply(context.Background(), reader); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	ctx := context.Background()

	// The volume must now address the replicated filesystem, with the rest of the handle
	// untouched including its doubled separator.
	pv, err := target.CoreV1().PersistentVolumes().Get(ctx, "pv-dir", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("restored PV missing: %v", err)
	}
	if pv.Spec.CSI.VolumeHandle != "weka/v2/testfs-replica//csi-volumes/vol-abc" {
		t.Errorf("volumeHandle = %q, want the filesystem renamed and nothing else changed", pv.Spec.CSI.VolumeHandle)
	}
	if pv.Spec.StorageClassName != "sc-dir-dr" {
		t.Errorf("PV storageClassName = %q, want sc-dir-dr", pv.Spec.StorageClassName)
	}
	if pv.Spec.ClaimRef.Namespace != "dr-default" {
		t.Errorf("claimRef namespace = %q, want dr-default", pv.Spec.ClaimRef.Namespace)
	}
	if pv.Spec.CSI.NodePublishSecretRef.Namespace != "weka-dr" {
		t.Errorf("secretRef namespace = %q, want weka-dr", pv.Spec.CSI.NodePublishSecretRef.Namespace)
	}
	if pv.Annotations["migrated-from"] != "prod-us-east" {
		t.Errorf("annotation not applied: %v", pv.Annotations)
	}

	// The claim must land in the mapped namespace, still pinned to its volume.
	claim, err := target.CoreV1().PersistentVolumeClaims("dr-default").Get(ctx, "pvc-dir", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("restored PVC missing from the mapped namespace: %v", err)
	}
	if claim.Spec.VolumeName != "pv-dir" {
		t.Errorf("claim volumeName = %q, want pv-dir", claim.Spec.VolumeName)
	}
	if *claim.Spec.StorageClassName != "sc-dir-dr" {
		t.Errorf("claim storageClassName = %q, want sc-dir-dr", *claim.Spec.StorageClassName)
	}

	// The class must agree with the volume about the filesystem.
	class, err := target.StorageV1().StorageClasses().Get(ctx, "sc-dir-dr", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("renamed StorageClass missing: %v", err)
	}
	if class.Parameters["filesystemName"] != "testfs-replica" {
		t.Errorf("class filesystemName = %q, want testfs-replica: it disagrees with the volume handle",
			class.Parameters["filesystemName"])
	}
	if class.Parameters["csi.storage.k8s.io/provisioner-secret-namespace"] != "weka-dr" {
		t.Errorf("class secret namespace = %q, want weka-dr", class.Parameters["csi.storage.k8s.io/provisioner-secret-namespace"])
	}

	// The secret must exist where everything now points, carrying the DR credentials.
	sec, err := target.CoreV1().Secrets("weka-dr").Get(ctx, "csi-wekafs-api-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("relocated Secret missing: %v", err)
	}
	if string(sec.Data["password"]) != "dr-password" {
		t.Errorf("password = %q, want the value from the environment", sec.Data["password"])
	}
	if string(sec.Data["endpoints"]) != "10.20.30.40:14000" {
		t.Errorf("endpoints = %q, want the DR endpoint", sec.Data["endpoints"])
	}
	if string(sec.Data["username"]) != "admin" {
		t.Errorf("username = %q, want it left intact", sec.Data["username"])
	}
}

// TestTransformCollisionIsRefused covers collapsing namespaces where claim names are not
// unique: the import must refuse up front rather than half-populate the cluster.
func TestTransformCollisionIsRefused(t *testing.T) {
	// Both namespaces hold a claim, and collapsing them creates two "pvc-dir"/"pvc-fs"
	// pairs only if names repeat; give them the same name to force it.
	cluster := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", UID: "uid"}},
		secret("csi-wekafs", "csi-wekafs-api-secret"),
		storageClass("sc-dir", "testfs"),
		dynamicPV("pv-a", "weka/v2/testfs/a", "sc-dir", "ns-a", "shared-name"),
		boundPVC("ns-a", "shared-name", "sc-dir", "pv-a"),
		dynamicPV("pv-b", "weka/v2/testfs/b", "sc-dir", "ns-b", "shared-name"),
		boundPVC("ns-b", "shared-name", "sc-dir", "pv-b"),
	)
	reader := openArchive(t, exportTo(t, cluster, collect.Options{IncludeSecretData: true}, ""), "")

	cfg, err := transform.ParseConfig([]byte("targetNamespace: merged\n"))
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	chain, err := transform.NewChainFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewChainFromConfig returned error: %v", err)
	}

	target := fake.NewSimpleClientset()
	_, err = apply.New(target, apply.Options{Transform: chain}).Apply(context.Background(), reader)
	if err == nil {
		t.Fatal("colliding claims were imported")
	}
	if !strings.Contains(err.Error(), "same identity") {
		t.Errorf("error does not explain the collision: %v", err)
	}

	// Nothing may have been created.
	claims, _ := target.CoreV1().PersistentVolumeClaims("merged").List(context.Background(), metav1.ListOptions{})
	if len(claims.Items) != 0 {
		t.Errorf("the refused import still created %d claim(s)", len(claims.Items))
	}
}
