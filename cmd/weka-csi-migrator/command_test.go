package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// fakeConnector hands the commands a fake cluster instead of a kubeconfig.
type fakeConnector struct{ c kubernetes.Interface }

func (f fakeConnector) client() (kubernetes.Interface, string, error) {
	return f.c, "test-context", nil
}

func testCluster() kubernetes.Interface {
	className := "sc-dir"
	return fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", UID: "cluster-uid"}},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "csi-wekafs", Name: "api-secret"},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"username":  []byte("admin"),
				"password":  []byte("super-secret-password"),
				"endpoints": []byte("172.31.41.54:14000"),
			},
		},
		&storagev1.StorageClass{
			ObjectMeta:  metav1.ObjectMeta{Name: className},
			Provisioner: "csi.weka.io",
			Parameters: map[string]string{
				"filesystemName": "testfs",
				"csi.storage.k8s.io/provisioner-secret-name":      "api-secret",
				"csi.storage.k8s.io/provisioner-secret-namespace": "csi-wekafs",
			},
		},
		&corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "pv-dir",
				Annotations: map[string]string{"pv.kubernetes.io/provisioned-by": "csi.weka.io"},
			},
			Spec: corev1.PersistentVolumeSpec{
				Capacity:         corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
				StorageClassName: className,
				ClaimRef: &corev1.ObjectReference{
					APIVersion: "v1", Kind: "PersistentVolumeClaim",
					Namespace: "infra", Name: "pvc-dir", UID: "claim-uid",
				},
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "csi.weka.io",
						VolumeHandle: "weka/v2/testfs//csi-volumes/vol-abc",
					},
				},
			},
		},
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Namespace: "infra", Name: "pvc-dir", UID: "claim-uid"},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
				StorageClassName: &className,
				VolumeName:       "pv-dir",
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
				},
			},
		},
	)
}

// runCLI executes the real command tree against a fake cluster, returning stdout.
func runCLI(t *testing.T, cluster kubernetes.Interface, args ...string) (string, error) {
	t.Helper()

	root := newRootCommand()
	// Rebuild the subcommands against the fake cluster, keeping the real flag wiring.
	root.RemoveCommand(root.Commands()...)
	connector := fakeConnector{c: cluster}
	root.AddCommand(newExportCommand(connector), newImportCommand(connector), newListCommand(), newShowCommand())

	out := &strings.Builder{}
	root.SetOut(out)
	root.SetErr(stderr)
	root.SetArgs(args)

	err := root.Execute()
	return out.String(), err
}

// TestExportWithSecretDataProducesAnEncryptedArchive is the end-to-end check that was
// missing: every earlier encryption test drove archive.Writer directly, so nothing verified
// that the export *command* actually passes the password down to it.
func TestExportWithSecretDataProducesAnEncryptedArchive(t *testing.T) {
	captureLogs(t)
	archivePath := filepath.Join(t.TempDir(), "test.wcsi")
	t.Setenv(passwordEnvVar, "hunter2")

	if _, err := runCLI(t, testCluster(), "export", "-o", archivePath,
		"--namespace", "infra", "--include-secret-data"); err != nil {
		t.Fatalf("export returned error: %v", err)
	}

	raw, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}

	// The credential must not be recoverable by grepping the file.
	if strings.Contains(string(raw), "super-secret-password") {
		t.Error("the archive contains the password in plaintext")
	}
	if !strings.Contains(string(raw), `"encrypted":true`) {
		t.Errorf("the archive header does not declare encryption: %.200q", raw)
	}
}

// TestListRefusesEncryptedArchiveWithoutPassword is the reported symptom: an archive
// exported with a password must not be readable without one.
func TestListRefusesEncryptedArchiveWithoutPassword(t *testing.T) {
	captureLogs(t)
	notATerminal(t)
	archivePath := filepath.Join(t.TempDir(), "test.wcsi")

	t.Setenv(passwordEnvVar, "hunter2")
	if _, err := runCLI(t, testCluster(), "export", "-o", archivePath,
		"--namespace", "infra", "--include-secret-data"); err != nil {
		t.Fatalf("export returned error: %v", err)
	}

	// Now try to read it with no password available at all.
	os.Unsetenv(passwordEnvVar)
	if _, err := runCLI(t, testCluster(), "list", archivePath); err == nil {
		t.Fatal("an encrypted archive was listed without a password")
	}

	// And with the right one it must open.
	t.Setenv(passwordEnvVar, "hunter2")
	out, err := runCLI(t, testCluster(), "list", archivePath)
	if err != nil {
		t.Fatalf("list with the correct password returned error: %v", err)
	}
	if !strings.Contains(out, "pv-dir") {
		t.Errorf("list output does not mention the exported volume: %q", out)
	}
}

// TestExportWithoutSecretDataIsUnencryptedByDefault documents the case most likely to be
// mistaken for a bug: a redacted export carries no credentials, so it is not encrypted
// unless a password is explicitly supplied.
func TestExportWithoutSecretDataIsUnencryptedByDefault(t *testing.T) {
	captureLogs(t)
	notATerminal(t)
	archivePath := filepath.Join(t.TempDir(), "plain.wcsi")

	if _, err := runCLI(t, testCluster(), "export", "-o", archivePath, "--namespace", "infra"); err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	raw, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	if !strings.Contains(string(raw), `"encrypted":false`) {
		t.Error("a redacted export without a password should not be encrypted")
	}
	// The credential must still be absent, because it was redacted rather than encrypted.
	if strings.Contains(string(raw), "super-secret-password") {
		t.Error("a redacted export leaked the password")
	}
}

// TestEncryptFlagEncryptsARedactedExport covers wanting confidentiality without embedding
// credentials: --encrypt asks for a password even though --include-secret-data was not given.
func TestEncryptFlagEncryptsARedactedExport(t *testing.T) {
	captureLogs(t)
	archivePath := filepath.Join(t.TempDir(), "enc.wcsi")
	t.Setenv(passwordEnvVar, "hunter2")

	if _, err := runCLI(t, testCluster(), "export", "-o", archivePath,
		"--namespace", "infra", "--encrypt"); err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	raw, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	if !strings.Contains(string(raw), `"encrypted":true`) {
		t.Error("--encrypt did not encrypt the archive")
	}
}

// TestShowPrintsApplicableManifests covers the "how do I know the archive is valid" path:
// show must emit the real object YAML, in apply order, ready to pipe into kubectl.
func TestShowPrintsApplicableManifests(t *testing.T) {
	captureLogs(t)
	notATerminal(t)
	archivePath := filepath.Join(t.TempDir(), "plain.wcsi")

	if _, err := runCLI(t, testCluster(), "export", "-o", archivePath, "--namespace", "infra"); err != nil {
		t.Fatalf("export returned error: %v", err)
	}

	out, err := runCLI(t, testCluster(), "show", archivePath)
	if err != nil {
		t.Fatalf("show returned error: %v", err)
	}

	// The volume handle is the whole point of the archive, so it must be visible.
	if !strings.Contains(out, "weka/v2/testfs//csi-volumes/vol-abc") {
		t.Errorf("show output does not contain the volume handle:\n%s", out)
	}
	// Documents must be separated so the stream parses as multi-document YAML.
	if !strings.Contains(out, "\n---\n") {
		t.Error("show output is not a multi-document YAML stream")
	}
	// Apply order: dependencies must precede the objects that reference them.
	secretAt := strings.Index(out, "kind: Secret")
	pvAt := strings.Index(out, "kind: PersistentVolume\n")
	pvcAt := strings.Index(out, "kind: PersistentVolumeClaim")
	if secretAt < 0 || pvAt < 0 || pvcAt < 0 {
		t.Fatalf("show output is missing expected kinds:\n%s", out)
	}
	if !(secretAt < pvAt && pvAt < pvcAt) {
		t.Error("show output is not in apply order")
	}
}

func TestShowFiltersByKind(t *testing.T) {
	captureLogs(t)
	notATerminal(t)
	archivePath := filepath.Join(t.TempDir(), "plain.wcsi")

	if _, err := runCLI(t, testCluster(), "export", "-o", archivePath, "--namespace", "infra"); err != nil {
		t.Fatalf("export returned error: %v", err)
	}

	out, err := runCLI(t, testCluster(), "show", archivePath, "--kind", "PersistentVolume")
	if err != nil {
		t.Fatalf("show returned error: %v", err)
	}
	if !strings.Contains(out, "kind: PersistentVolume") {
		t.Error("filtered output is missing the requested kind")
	}
	if strings.Contains(out, "kind: Secret") {
		t.Error("filtered output contains a kind that was not requested")
	}

	// An empty selection is an error rather than silent success, so a typo in a filter is
	// not mistaken for an empty archive.
	if _, err := runCLI(t, testCluster(), "show", archivePath, "--kind", "Nonexistent"); err == nil {
		t.Error("a filter matching nothing was reported as success")
	}
}

func TestShowExtractsToDirectory(t *testing.T) {
	captureLogs(t)
	notATerminal(t)
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "plain.wcsi")
	extractDir := filepath.Join(dir, "extracted")

	if _, err := runCLI(t, testCluster(), "export", "-o", archivePath, "--namespace", "infra"); err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	if _, err := runCLI(t, testCluster(), "show", archivePath, "--output-dir", extractDir); err != nil {
		t.Fatalf("show --output-dir returned error: %v", err)
	}

	pv := filepath.Join(extractDir, "objects", "persistentvolumes", "pv-dir.yaml")
	body, err := os.ReadFile(pv)
	if err != nil {
		t.Fatalf("expected extracted file %s: %v", pv, err)
	}
	if !strings.Contains(string(body), "weka/v2/testfs//csi-volumes/vol-abc") {
		t.Error("extracted PersistentVolume does not contain its volume handle")
	}

	// Extracted files may hold credentials, so they must not be world-readable.
	info, err := os.Stat(pv)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("extracted file mode = %o, want 600", perm)
	}
}

// exportTo runs an export into path, returning the command error.
func exportTo(t *testing.T, path string, extraArgs ...string) error {
	t.Helper()
	args := append([]string{"export", "-o", path, "--namespace", "infra"}, extraArgs...)
	_, err := runCLI(t, testCluster(), args...)
	return err
}

// TestExportRefusesToOverwriteWithoutForce protects an archive that may be the only record
// of a cluster's volumes.
func TestExportRefusesToOverwriteWithoutForce(t *testing.T) {
	captureLogs(t)
	notATerminal(t)
	archivePath := filepath.Join(t.TempDir(), "cluster.wcsi")

	if err := exportTo(t, archivePath); err != nil {
		t.Fatalf("first export returned error: %v", err)
	}
	original, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}

	err = exportTo(t, archivePath)
	if err == nil {
		t.Fatal("the second export overwrote an existing archive")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error does not mention the way forward: %v", err)
	}

	// The existing archive must be byte-for-byte untouched.
	after, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	if !bytes.Equal(original, after) {
		t.Error("the refused export still modified the existing archive")
	}
}

func TestExportForceOverwrites(t *testing.T) {
	captureLogs(t)
	notATerminal(t)
	archivePath := filepath.Join(t.TempDir(), "cluster.wcsi")

	if err := exportTo(t, archivePath); err != nil {
		t.Fatalf("first export returned error: %v", err)
	}
	// Make the existing content unmistakably different from a real archive.
	if err := os.WriteFile(archivePath, []byte("stale contents"), 0o600); err != nil {
		t.Fatalf("seeding stale archive: %v", err)
	}

	if err := exportTo(t, archivePath, "--force"); err != nil {
		t.Fatalf("--force export returned error: %v", err)
	}
	raw, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	if string(raw) == "stale contents" {
		t.Error("--force did not replace the existing file")
	}
	if !bytes.HasPrefix(raw, []byte("WEKACSI")) {
		t.Errorf("the overwritten file is not an archive: %.40q", raw)
	}
}

func TestExportForceShorthand(t *testing.T) {
	captureLogs(t)
	notATerminal(t)
	archivePath := filepath.Join(t.TempDir(), "cluster.wcsi")

	if err := exportTo(t, archivePath); err != nil {
		t.Fatalf("first export returned error: %v", err)
	}
	if err := exportTo(t, archivePath, "-f"); err != nil {
		t.Errorf("-f was not accepted as shorthand for --force: %v", err)
	}
}

// TestExportLeavesNoStagingFiles covers the staging mechanism: a successful export must
// leave exactly the archive behind, not the temporary file it was written through.
func TestExportLeavesNoStagingFiles(t *testing.T) {
	captureLogs(t)
	notATerminal(t)
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "cluster.wcsi")

	if err := exportTo(t, archivePath); err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "cluster.wcsi" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want only cluster.wcsi", names)
	}
}

// TestFailedExportLeavesTargetIntact is why the archive is staged rather than written in
// place: a failure partway must not leave a truncated file that still looks like a backup.
func TestFailedExportLeavesTargetIntact(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "cluster.wcsi")
	if err := os.WriteFile(archivePath, []byte("previous good archive"), 0o600); err != nil {
		t.Fatalf("seeding archive: %v", err)
	}

	out, err := openOutput(archivePath, true)
	if err != nil {
		t.Fatalf("openOutput returned error: %v", err)
	}
	if _, err := out.Write([]byte("half an archive")); err != nil {
		t.Fatalf("write returned error: %v", err)
	}
	// Abandon without committing, as a failed WriteTo would.
	out.cleanup()

	raw, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	if string(raw) != "previous good archive" {
		t.Errorf("an abandoned export damaged the existing archive: %q", raw)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("staging file was left behind: %d entries", len(entries))
	}
}

// TestExportChecksOutputBeforeCollecting keeps a doomed export from doing a full cluster
// collection first. The fake cluster records whether it was consulted.
func TestExportChecksOutputBeforeCollecting(t *testing.T) {
	captureLogs(t)
	notATerminal(t)
	archivePath := filepath.Join(t.TempDir(), "cluster.wcsi")
	if err := os.WriteFile(archivePath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("seeding archive: %v", err)
	}

	cluster := fake.NewSimpleClientset()
	var consulted bool
	cluster.PrependReactor("*", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		consulted = true
		return false, nil, nil
	})

	_, err := runCLI(t, cluster, "export", "-o", archivePath)
	if err == nil {
		t.Fatal("export overwrote an existing archive")
	}
	if consulted {
		t.Error("the cluster was queried before the output path was checked")
	}
}
