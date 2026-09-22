package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"sort"
	"time"
)

// Writer accumulates objects and then emits a container. Exports are inventories of
// Kubernetes metadata rather than bulk data, so the whole container is assembled in memory:
// it keeps the manifest digest, the tar and the encryption framing trivially consistent.
type Writer struct {
	manifest Manifest
	bodies   map[string][]byte
}

// NewWriter starts a container. The tool string is recorded in the manifest for support.
func NewWriter(tool, driverName string) *Writer {
	return &Writer{
		manifest: Manifest{
			FormatVersion: FormatVersion,
			CreatedAt:     time.Now().UTC(),
			Tool:          tool,
			DriverName:    driverName,
		},
		bodies: make(map[string][]byte),
	}
}

// SetSource records the cluster an export came from.
func (w *Writer) SetSource(src SourceCluster) { w.manifest.Source = src }

// SetNamespace records that the export was restricted to a single namespace.
func (w *Writer) SetNamespace(ns string) { w.manifest.Namespace = ns }

// SetSecretDisposition records whether sensitive Secret keys were exported verbatim, and
// which keys were scrubbed when they were not.
func (w *Writer) SetSecretDisposition(included bool, redacted map[string][]string) {
	w.manifest.SecretsIncluded = included
	if len(redacted) > 0 {
		w.manifest.RedactedSecretKeys = redacted
	}
}

// AddVolumeRecord appends a summary of one exported PersistentVolume.
func (w *Writer) AddVolumeRecord(v VolumeRecord) {
	w.manifest.Volumes = append(w.manifest.Volumes, v)
}

// AddWarning records a non-fatal finding to be replayed at list and import time.
func (w *Writer) AddWarning(format string, args ...any) {
	w.manifest.Warnings = append(w.manifest.Warnings, fmt.Sprintf(format, args...))
}

// Warnings returns the warnings recorded so far.
func (w *Writer) Warnings() []string { return w.manifest.Warnings }

// Add stores one object's YAML document under path. Paths must be unique.
func (w *Writer) Add(path, apiVersion, kind, namespace, name string, yamlDoc []byte) error {
	if path == "" {
		return fmt.Errorf("cannot add object %q with an empty path", name)
	}
	if _, exists := w.bodies[path]; exists {
		return fmt.Errorf("duplicate object path %q", path)
	}
	body := make([]byte, len(yamlDoc))
	copy(body, yamlDoc)
	w.bodies[path] = body
	w.manifest.Entries = append(w.manifest.Entries, Entry{
		Path:       path,
		APIVersion: apiVersion,
		Kind:       kind,
		Namespace:  namespace,
		Name:       name,
		Size:       int64(len(body)),
		Sum:        sha256Hex(body),
	})
	return nil
}

// Len reports how many objects have been added.
func (w *Writer) Len() int { return len(w.bodies) }

// Manifest exposes the manifest as it currently stands, for callers that want to report on
// an export without writing it.
func (w *Writer) Manifest() Manifest { return w.manifest }

// WriteTo serialises the container. Supplying a password encrypts and authenticates it;
// omitting one produces a plain gzipped tar that any standard tool can open.
func (w *Writer) WriteTo(out io.Writer, password string) error {
	// Sort for reproducible output: two exports of an unchanged cluster should differ only
	// in their timestamps.
	sort.Slice(w.manifest.Entries, func(i, j int) bool {
		return w.manifest.Entries[i].Path < w.manifest.Entries[j].Path
	})
	sort.Slice(w.manifest.Volumes, func(i, j int) bool {
		return w.manifest.Volumes[i].PVName < w.manifest.Volumes[j].PVName
	})

	manifestJSON, err := marshalManifest(&w.manifest)
	if err != nil {
		return err
	}

	payload, err := w.buildPayload(manifestJSON)
	if err != nil {
		return err
	}

	header := &Header{
		FormatVersion: FormatVersion,
		Encrypted:     password != "",
		ManifestSum:   sha256Hex(manifestJSON),
		CreatedAt:     w.manifest.CreatedAt.Format(time.RFC3339),
		Tool:          w.manifest.Tool,
	}
	if header.Encrypted {
		if header.KDF, err = newKDFParams(); err != nil {
			return err
		}
	}
	headerJSON, err := header.marshal()
	if err != nil {
		return err
	}

	if _, err := out.Write([]byte(Magic + "\n")); err != nil {
		return fmt.Errorf("writing magic: %w", err)
	}
	if _, err := out.Write(append(headerJSON, '\n')); err != nil {
		return fmt.Errorf("writing header: %w", err)
	}

	if !header.Encrypted {
		if _, err := out.Write(payload); err != nil {
			return fmt.Errorf("writing payload: %w", err)
		}
		return nil
	}

	key, err := deriveKey(password, header.KDF)
	if err != nil {
		return err
	}
	return encryptPayload(out, payload, key, sha256Raw(headerJSON))
}

// buildPayload produces the gzipped tar holding the manifest and every object.
func (w *Writer) buildPayload(manifestJSON []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	// The manifest goes first so that a reader can stop as soon as it has the inventory.
	if err := writeTarFile(tw, ManifestPath, manifestJSON); err != nil {
		return nil, err
	}
	for _, entry := range w.manifest.Entries {
		if err := writeTarFile(tw, entry.Path, w.bodies[entry.Path]); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("finalising tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("finalising gzip: %w", err)
	}
	return buf.Bytes(), nil
}

func writeTarFile(tw *tar.Writer, path string, body []byte) error {
	// ModTime is deliberately zero: the manifest already carries the creation time, and a
	// per-file timestamp would make otherwise identical exports differ.
	hdr := &tar.Header{
		Name:     path,
		Mode:     0o600,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("writing tar header for %q: %w", path, err)
	}
	if _, err := tw.Write(body); err != nil {
		return fmt.Errorf("writing tar body for %q: %w", path, err)
	}
	return nil
}
