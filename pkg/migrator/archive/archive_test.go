package archive

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

func sampleWriter(t *testing.T) *Writer {
	t.Helper()
	w := NewWriter("weka-csi-migrator/test", "csi.weka.io")
	w.SetSource(SourceCluster{KubeSystemUID: "uid-1", ServerVersion: "v1.31.0", Context: "src"})
	if err := w.Add("objects/persistentvolumes/pv-a.yaml", "v1", "PersistentVolume", "", "pv-a", []byte("kind: PersistentVolume\n")); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if err := w.Add("objects/persistentvolumeclaims/ns1/pvc-a.yaml", "v1", "PersistentVolumeClaim", "ns1", "pvc-a", []byte("kind: PersistentVolumeClaim\n")); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	w.AddVolumeRecord(VolumeRecord{
		PVName: "pv-a", VolumeHandle: "weka/v2/fs1/csi-volumes/vol-a",
		FilesystemName: "fs1", Backing: "directory", PortableAcrossWekaClusters: true,
	})
	return w
}

func writeArchive(t *testing.T, password string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := sampleWriter(t).WriteTo(&buf, password); err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}
	return buf.Bytes()
}

func TestRoundTripPlain(t *testing.T) {
	raw := writeArchive(t, "")
	r, warnings, err := Open(bytes.NewReader(raw), OpenOptions{})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if r.Header.Encrypted {
		t.Error("plain archive reports itself as encrypted")
	}
	if len(r.Entries()) != 2 {
		t.Fatalf("got %d entries, want 2", len(r.Entries()))
	}
	body, ok := r.Body("objects/persistentvolumes/pv-a.yaml")
	if !ok {
		t.Fatal("pv-a body missing")
	}
	if string(body) != "kind: PersistentVolume\n" {
		t.Errorf("body = %q, want %q", body, "kind: PersistentVolume\n")
	}
	if r.Manifest.Volumes[0].FilesystemName != "fs1" {
		t.Errorf("volume record lost: %+v", r.Manifest.Volumes)
	}
}

func TestRoundTripEncrypted(t *testing.T) {
	raw := writeArchive(t, "correct horse battery staple")
	r, _, err := Open(bytes.NewReader(raw), OpenOptions{Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if !r.Header.Encrypted {
		t.Error("encrypted archive does not report itself as encrypted")
	}
	if _, ok := r.Body("objects/persistentvolumeclaims/ns1/pvc-a.yaml"); !ok {
		t.Error("pvc-a body missing")
	}
}

// TestEncryptedArchiveHidesContents guards against the payload leaking in plaintext, which
// is the entire point of exporting with --include-secret-data.
func TestEncryptedArchiveHidesContents(t *testing.T) {
	raw := writeArchive(t, "hunter2")
	if bytes.Contains(raw, []byte("PersistentVolumeClaim")) {
		t.Error("encrypted archive contains plaintext object data")
	}
	if !bytes.HasPrefix(raw, []byte(Magic)) {
		t.Error("encrypted archive does not start with the magic")
	}
}

func TestWrongPassword(t *testing.T) {
	raw := writeArchive(t, "right")
	_, _, err := Open(bytes.NewReader(raw), OpenOptions{Password: "wrong"})
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("got %v, want ErrIntegrity", err)
	}
}

func TestPasswordExpectations(t *testing.T) {
	if _, _, err := Open(bytes.NewReader(writeArchive(t, "pw")), OpenOptions{}); !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("opening encrypted archive without a password: got %v, want ErrPasswordRequired", err)
	}
	if _, _, err := Open(bytes.NewReader(writeArchive(t, "")), OpenOptions{Password: "pw"}); !errors.Is(err, ErrPasswordNotNeeded) {
		t.Errorf("opening plain archive with a password: got %v, want ErrPasswordNotNeeded", err)
	}
}

func TestNotAnArchive(t *testing.T) {
	for _, input := range []string{"", "hello\n", "WEKACSI9\n{}\n"} {
		if _, _, err := Open(strings.NewReader(input), OpenOptions{}); err == nil {
			t.Errorf("Open(%q) unexpectedly succeeded", input)
		}
	}
}

// TestTamperedPlainPayloadIsDetected covers the tamper-evidence guarantee for unencrypted
// archives: an edited object must not import silently.
func TestTamperedPlainPayloadIsDetected(t *testing.T) {
	raw := writeArchive(t, "")
	// Flip a byte deep in the gzip payload, past the header lines.
	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)-8] ^= 0xff

	_, _, err := Open(bytes.NewReader(tampered), OpenOptions{})
	if err == nil {
		t.Fatal("tampered archive opened without error")
	}
}

// TestTamperedManifestSumIsDetected simulates an editor that rewrote an object and adjusted
// nothing else: the manifest digest recorded in the header must catch it.
func TestTamperedManifestSumIsDetected(t *testing.T) {
	raw := writeArchive(t, "")
	tampered := bytes.Replace(raw, []byte(`"manifestSum":"`), []byte(`"manifestSum":"0`), 1)
	if bytes.Equal(raw, tampered) {
		t.Fatal("test could not find manifestSum in the header")
	}
	_, _, err := Open(bytes.NewReader(tampered), OpenOptions{})
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("got %v, want ErrIntegrity", err)
	}
}

func TestIgnoreIntegrityErrorsRecoversPlainArchive(t *testing.T) {
	raw := writeArchive(t, "")
	tampered := bytes.Replace(raw, []byte(`"manifestSum":"`), []byte(`"manifestSum":"0`), 1)

	r, warnings, err := Open(bytes.NewReader(tampered), OpenOptions{IgnoreIntegrityErrors: true})
	if err != nil {
		t.Fatalf("Open with IgnoreIntegrityErrors returned error: %v", err)
	}
	if len(warnings) == 0 {
		t.Error("expected a warning describing the digest mismatch")
	}
	if len(r.Entries()) != 2 {
		t.Errorf("got %d entries, want 2", len(r.Entries()))
	}
}

// TestEncryptedArchiveIsTamperProof confirms that IgnoreIntegrityErrors cannot be used to
// bypass authentication on an encrypted container.
func TestEncryptedArchiveIsTamperProof(t *testing.T) {
	raw := writeArchive(t, "pw")
	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)-1] ^= 0xff

	if _, _, err := Open(bytes.NewReader(tampered), OpenOptions{Password: "pw", IgnoreIntegrityErrors: true}); err == nil {
		t.Fatal("tampered encrypted archive opened despite authentication")
	}
}

// TestTruncatedEncryptedArchiveIsDetected covers the frame terminator: dropping trailing
// frames must not yield a valid shorter archive.
func TestTruncatedEncryptedArchiveIsDetected(t *testing.T) {
	w := NewWriter("weka-csi-migrator/test", "csi.weka.io")
	// Incompressible, so that the payload genuinely spans several frames after gzip and
	// truncation removes a whole frame rather than merely corrupting one.
	big := make([]byte, 200*1024)
	if _, err := rand.Read(big); err != nil {
		t.Fatalf("generating test data: %v", err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := w.Add("objects/x/"+name+".yaml", "v1", "ConfigMap", "ns", name, big); err != nil {
			t.Fatalf("Add returned error: %v", err)
		}
	}
	var buf bytes.Buffer
	if err := w.WriteTo(&buf, "pw"); err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}
	raw := buf.Bytes()

	if _, _, err := Open(bytes.NewReader(raw[:len(raw)-4096]), OpenOptions{Password: "pw"}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("got %v, want ErrIntegrity", err)
	}
}

func TestAppendedDataIsDetected(t *testing.T) {
	raw := writeArchive(t, "pw")
	appended := append(append([]byte(nil), raw...), []byte("extra")...)
	if _, _, err := Open(bytes.NewReader(appended), OpenOptions{Password: "pw"}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("got %v, want ErrIntegrity", err)
	}
}

func TestDuplicatePathRejected(t *testing.T) {
	w := NewWriter("t", "csi.weka.io")
	if err := w.Add("a.yaml", "v1", "Secret", "ns", "s", []byte("x")); err != nil {
		t.Fatalf("first Add returned error: %v", err)
	}
	if err := w.Add("a.yaml", "v1", "Secret", "ns", "s", []byte("y")); err == nil {
		t.Error("duplicate path was accepted")
	}
}

// TestOutputIsReproducible keeps exports diffable: the same input must serialise
// identically apart from the timestamp and, when encrypted, the random salt.
func TestOutputIsReproducible(t *testing.T) {
	a := sampleWriter(t)
	b := sampleWriter(t)
	b.manifest.CreatedAt = a.manifest.CreatedAt

	var bufA, bufB bytes.Buffer
	if err := a.WriteTo(&bufA, ""); err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}
	if err := b.WriteTo(&bufB, ""); err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}
	if !bytes.Equal(bufA.Bytes(), bufB.Bytes()) {
		t.Error("two identical exports produced different bytes")
	}
}
