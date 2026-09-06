// Package archive implements the .wcsi container format used by weka-csi-migrator.
//
// # Layout
//
// A container is a plaintext header followed by a payload:
//
//	WEKACSI1\n
//	{"formatVersion":1,"encrypted":false,...}\n
//	<payload>
//
// The payload is a gzipped tar holding manifest.json plus one YAML document per exported
// object. When the container is encrypted the payload is a sequence of AES-256-GCM frames
// instead; the header stays readable so that tooling can report why it cannot open a file.
//
// # Integrity
//
// Two different guarantees apply, and the difference is deliberate:
//
//   - Encrypted containers are tamper-proof. Every frame is authenticated by AES-GCM, and
//     the header digest is bound in as additional authenticated data, so neither the
//     payload nor the header can be altered without the password.
//   - Unencrypted containers are only tamper-evident. The manifest records a SHA-256 per
//     entry and the header records a SHA-256 of the manifest, which reliably detects
//     corruption and accidental edits, but an attacker able to rewrite the file can
//     recompute those digests. Use a password when that matters.
//
// Import verifies whichever guarantee applies before applying a single object.
package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Magic identifies the container and its major format generation.
const Magic = "WEKACSI1"

// FormatVersion is the current container format version. Readers reject anything newer.
const FormatVersion = 1

// ManifestPath is the location of the manifest inside the tar payload.
const ManifestPath = "manifest.json"

// frameSize is the plaintext chunk size for encrypted payloads.
const frameSize = 64 * 1024

// ErrNotAnArchive indicates the file does not begin with the expected magic.
var ErrNotAnArchive = errors.New("not a weka-csi-migrator archive")

// ErrUnsupportedVersion indicates the container was written by a newer tool.
var ErrUnsupportedVersion = errors.New("unsupported archive format version")

// ErrPasswordRequired indicates the container is encrypted but no password was supplied.
var ErrPasswordRequired = errors.New("archive is encrypted: a password is required")

// ErrPasswordNotNeeded indicates a password was supplied for an unencrypted container.
var ErrPasswordNotNeeded = errors.New("archive is not encrypted: no password should be supplied")

// ErrIntegrity indicates the container failed verification. Callers may choose to override
// this, but only for unencrypted containers: an encrypted container that fails cannot be
// decrypted at all.
var ErrIntegrity = errors.New("archive integrity check failed")

// KDFParams records the argon2id cost parameters used to derive the container key. They are
// stored per-container so that future increases do not orphan existing archives.
type KDFParams struct {
	Time    uint32 `json:"time"`
	MemoryK uint32 `json:"memoryKiB"`
	Threads uint8  `json:"threads"`
	SaltB64 string `json:"salt"`
}

// Header is the plaintext preamble of a container.
type Header struct {
	FormatVersion int        `json:"formatVersion"`
	Encrypted     bool       `json:"encrypted"`
	KDF           *KDFParams `json:"kdf,omitempty"`
	// ManifestSum is the hex SHA-256 of manifest.json. It is the root of the
	// tamper-evidence chain for unencrypted containers.
	ManifestSum string `json:"manifestSum"`
	// CreatedAt is informational only and never participates in verification.
	CreatedAt string `json:"createdAt"`
	// Tool records the producing binary version, to aid support.
	Tool string `json:"tool"`
}

// Entry describes one exported object inside the payload.
type Entry struct {
	// Path is the tar-relative location, e.g. "objects/persistentvolumes/pv-foo.yaml".
	Path string `json:"path"`
	// APIVersion, Kind, Namespace and Name identify the object without opening the YAML,
	// which is what makes `weka-csi-migrator list` cheap.
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	// Size and Sum are the plaintext length and hex SHA-256 of the YAML document.
	Size int64  `json:"size"`
	Sum  string `json:"sum"`
}

// VolumeRecord summarises one exported PersistentVolume, so that `list` can show what a
// container holds and a future transform file can be authored against it without decoding
// every object.
type VolumeRecord struct {
	PVName         string `json:"pvName"`
	PVCNamespace   string `json:"pvcNamespace,omitempty"`
	PVCName        string `json:"pvcName,omitempty"`
	StorageClass   string `json:"storageClass,omitempty"`
	VolumeHandle   string `json:"volumeHandle"`
	FilesystemName string `json:"filesystemName"`
	Backing        string `json:"backing"`
	Capacity       string `json:"capacity,omitempty"`
	// PortableAcrossWekaClusters is false for snapshot-backed volumes, which Weka cannot
	// replicate to another cluster.
	PortableAcrossWekaClusters bool `json:"portableAcrossWekaClusters"`
}

// SourceCluster records where an export came from, for operator sanity when restoring.
type SourceCluster struct {
	// KubeSystemUID is the UID of the kube-system namespace, the conventional stable
	// identifier for a Kubernetes cluster.
	KubeSystemUID string `json:"kubeSystemUID,omitempty"`
	ServerVersion string `json:"serverVersion,omitempty"`
	Context       string `json:"context,omitempty"`
}

// Manifest is the inventory of a container. It is the first entry in the tar payload.
type Manifest struct {
	FormatVersion int           `json:"formatVersion"`
	CreatedAt     time.Time     `json:"createdAt"`
	Tool          string        `json:"tool"`
	DriverName    string        `json:"driverName"`
	Source        SourceCluster `json:"source"`
	// Namespace is the single namespace an export was restricted to, empty for whole
	// cluster exports.
	Namespace string `json:"namespace,omitempty"`
	// SecretsIncluded reports whether sensitive Secret keys were exported verbatim.
	// RedactedSecretKeys lists what was scrubbed when they were not.
	SecretsIncluded    bool                `json:"secretsIncluded"`
	RedactedSecretKeys map[string][]string `json:"redactedSecretKeys,omitempty"`
	Entries            []Entry             `json:"entries"`
	Volumes            []VolumeRecord      `json:"volumes,omitempty"`
	// Warnings records non-fatal findings, such as snapshot-backed volumes that cannot be
	// recreated against a different Weka cluster.
	Warnings []string `json:"warnings,omitempty"`
}

// Validate checks internal consistency of a decoded manifest.
func (m *Manifest) Validate() error {
	if m.FormatVersion == 0 {
		return fmt.Errorf("%w: manifest has no format version", ErrIntegrity)
	}
	if m.FormatVersion > FormatVersion {
		return fmt.Errorf("%w: manifest declares version %d, this tool supports up to %d",
			ErrUnsupportedVersion, m.FormatVersion, FormatVersion)
	}
	seen := make(map[string]struct{}, len(m.Entries))
	for _, e := range m.Entries {
		if e.Path == "" || e.Sum == "" {
			return fmt.Errorf("%w: manifest entry %q is incomplete", ErrIntegrity, e.Name)
		}
		if _, dup := seen[e.Path]; dup {
			return fmt.Errorf("%w: manifest lists %q twice", ErrIntegrity, e.Path)
		}
		seen[e.Path] = struct{}{}
	}
	return nil
}

func (h *Header) validate() error {
	if h.FormatVersion > FormatVersion {
		return fmt.Errorf("%w: archive declares version %d, this tool supports up to %d",
			ErrUnsupportedVersion, h.FormatVersion, FormatVersion)
	}
	if h.FormatVersion <= 0 {
		return fmt.Errorf("%w: archive declares no format version", ErrNotAnArchive)
	}
	if h.Encrypted && h.KDF == nil {
		return fmt.Errorf("%w: encrypted archive has no key derivation parameters", ErrNotAnArchive)
	}
	return nil
}

func (h *Header) marshal() ([]byte, error) {
	b, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("encoding archive header: %w", err)
	}
	return b, nil
}
