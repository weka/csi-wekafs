package archive

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxPayloadSize bounds decompression so that a malformed or hostile container cannot
// exhaust memory. Exports are object inventories; 512 MiB is far beyond any real cluster.
const maxPayloadSize = 512 << 20

// Reader is an opened container.
type Reader struct {
	Header   Header
	Manifest Manifest

	bodies map[string][]byte
}

// OpenOptions controls how strictly a container is verified.
type OpenOptions struct {
	// Password decrypts the container. Required for encrypted containers, rejected for
	// plain ones so that a mistyped invocation cannot silently produce a weaker result.
	Password string
	// IgnoreIntegrityErrors downgrades digest mismatches to warnings. It cannot rescue an
	// encrypted container, whose payload simply will not decrypt if altered. Reserved for
	// recovering data from a damaged archive.
	IgnoreIntegrityErrors bool
}

// Open reads and verifies a container. Every digest is checked before Open returns, so a
// caller that gets a Reader can apply its contents without further validation.
func Open(r io.Reader, opts OpenOptions) (*Reader, []string, error) {
	br := bufio.NewReader(r)

	magic, err := br.ReadString('\n')
	if err != nil || strings.TrimRight(magic, "\n") != Magic {
		return nil, nil, ErrNotAnArchive
	}
	headerLine, err := br.ReadString('\n')
	if err != nil {
		return nil, nil, fmt.Errorf("%w: header is truncated", ErrNotAnArchive)
	}
	headerJSON := []byte(strings.TrimRight(headerLine, "\n"))

	var header Header
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, nil, fmt.Errorf("%w: header is not valid JSON: %v", ErrNotAnArchive, err)
	}
	if err := header.validate(); err != nil {
		return nil, nil, err
	}
	switch {
	case header.Encrypted && opts.Password == "":
		return nil, nil, ErrPasswordRequired
	case !header.Encrypted && opts.Password != "":
		return nil, nil, ErrPasswordNotNeeded
	}

	var payload []byte
	if header.Encrypted {
		key, err := deriveKey(opts.Password, header.KDF)
		if err != nil {
			return nil, nil, err
		}
		if payload, err = decryptPayload(br, key, sha256Raw(headerJSON)); err != nil {
			return nil, nil, err
		}
	} else if payload, err = io.ReadAll(io.LimitReader(br, maxPayloadSize)); err != nil {
		return nil, nil, fmt.Errorf("reading payload: %w", err)
	}

	bodies, err := untar(payload)
	if err != nil {
		return nil, nil, err
	}

	manifestJSON, ok := bodies[ManifestPath]
	if !ok {
		return nil, nil, fmt.Errorf("%w: archive has no %s", ErrIntegrity, ManifestPath)
	}
	delete(bodies, ManifestPath)

	var warnings []string
	// For an encrypted container AES-GCM has already authenticated everything, so a
	// mismatch here would mean a bug rather than tampering; check it regardless, cheaply.
	if got := sha256Hex(manifestJSON); got != header.ManifestSum {
		problem := fmt.Errorf("%w: manifest digest is %s but the header records %s", ErrIntegrity, got, header.ManifestSum)
		if !opts.IgnoreIntegrityErrors {
			return nil, nil, problem
		}
		warnings = append(warnings, problem.Error())
	}

	var manifest Manifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, nil, fmt.Errorf("%w: manifest is not valid JSON: %v", ErrIntegrity, err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, nil, err
	}

	entryWarnings, err := verifyEntries(&manifest, bodies, opts.IgnoreIntegrityErrors)
	if err != nil {
		return nil, nil, err
	}
	warnings = append(warnings, entryWarnings...)

	return &Reader{Header: header, Manifest: manifest, bodies: bodies}, warnings, nil
}

// verifyEntries checks every manifest entry against the payload, in both directions: a
// missing object and an unlisted stowaway are equally suspect.
func verifyEntries(m *Manifest, bodies map[string][]byte, ignore bool) ([]string, error) {
	var warnings []string
	fail := func(err error) error {
		if ignore {
			warnings = append(warnings, err.Error())
			return nil
		}
		return err
	}

	listed := make(map[string]struct{}, len(m.Entries))
	for _, entry := range m.Entries {
		listed[entry.Path] = struct{}{}
		body, ok := bodies[entry.Path]
		if !ok {
			if err := fail(fmt.Errorf("%w: manifest lists %q but the archive does not contain it", ErrIntegrity, entry.Path)); err != nil {
				return nil, err
			}
			continue
		}
		if got := sha256Hex(body); got != entry.Sum {
			if err := fail(fmt.Errorf("%w: %q has digest %s but the manifest records %s", ErrIntegrity, entry.Path, got, entry.Sum)); err != nil {
				return nil, err
			}
		}
		if int64(len(body)) != entry.Size {
			if err := fail(fmt.Errorf("%w: %q is %d bytes but the manifest records %d", ErrIntegrity, entry.Path, len(body), entry.Size)); err != nil {
				return nil, err
			}
		}
	}
	for path := range bodies {
		if _, ok := listed[path]; !ok {
			if err := fail(fmt.Errorf("%w: archive contains %q which the manifest does not list", ErrIntegrity, path)); err != nil {
				return nil, err
			}
		}
	}
	return warnings, nil
}

func untar(payload []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: payload is not gzipped: %v", ErrIntegrity, err)
	}
	defer func() { _ = gz.Close() }()

	// Decompress fully before parsing. Reading the stream to EOF is what makes the gzip
	// reader verify its CRC32 and length trailer; a tar reader stops at the end-of-archive
	// marker and would leave corruption in the trailer undetected.
	decompressed, err := io.ReadAll(io.LimitReader(gz, maxPayloadSize))
	if err != nil {
		return nil, fmt.Errorf("%w: payload is corrupt: %v", ErrIntegrity, err)
	}

	bodies := make(map[string][]byte)
	tr := tar.NewReader(bytes.NewReader(decompressed))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: payload is not a valid tar: %v", ErrIntegrity, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Paths are used as map keys only and never touch the filesystem, but reject
		// traversal anyway so that a future caller cannot turn this into a write primitive.
		if strings.Contains(hdr.Name, "..") || strings.HasPrefix(hdr.Name, "/") {
			return nil, fmt.Errorf("%w: archive contains an unsafe path %q", ErrIntegrity, hdr.Name)
		}
		body, err := io.ReadAll(io.LimitReader(tr, maxPayloadSize))
		if err != nil {
			return nil, fmt.Errorf("%w: reading %q: %v", ErrIntegrity, hdr.Name, err)
		}
		bodies[hdr.Name] = body
	}
	return bodies, nil
}

// Body returns the YAML document stored at path.
func (r *Reader) Body(path string) ([]byte, bool) {
	body, ok := r.bodies[path]
	return body, ok
}

// Entries returns the manifest entries in archive order.
func (r *Reader) Entries() []Entry { return r.Manifest.Entries }

func marshalManifest(m *Manifest) ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding manifest: %w", err)
	}
	return b, nil
}
