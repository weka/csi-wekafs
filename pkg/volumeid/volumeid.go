// Package volumeid parses and constructs the CSI volume handles used by the Weka CSI driver.
//
// It is deliberately dependency-free (stdlib only) so that it can be shared between the
// driver itself (pkg/wekafs) and out-of-tree tooling such as the migrator, and so that it
// can be lifted into a standalone module without dragging the driver along.
//
// # Losslessness
//
// A volume handle is an opaque identifier as far as Kubernetes is concerned: whatever the
// driver emitted at provisioning time is what must be presented back at mount time. The
// handle generator does NOT normalise separators, so a cluster configured with an empty or
// slash-prefixed dynamicVolPath produces handles containing a double slash, for example
// "weka/v2/fs1//csi-volumes/vol-abc". Such handles are valid and in active use.
//
// Every function here therefore preserves the original bytes. Parse retains the input
// verbatim and String returns it unchanged; the only mutating helper, WithFilesystemName,
// performs a surgical splice at a recorded offset rather than re-assembling the handle from
// its components. Callers must never reconstruct a handle by concatenating parsed fields.
package volumeid

import (
	"errors"
	"fmt"
	"strings"
)

// Type is the versioned prefix of a volume handle, e.g. "weka/v2".
type Type string

const (
	// TypeDirV1 is the legacy directory-backed volume type. The filesystem name is supplied
	// by the StorageClass and the volume is a directory (with a quota) inside it.
	TypeDirV1 Type = "dir/v1"
	// TypeUnified is the current volume type, covering filesystem-, directory- and
	// snapshot-backed volumes alike.
	TypeUnified Type = "weka/v2"
	// TypeUnknown is returned for handles whose prefix matches no known type.
	TypeUnknown Type = "AMBIGUOUS_VOLUME_TYPE"
	// TypeEmpty is the zero value, used when a StorageClass omits volumeType.
	TypeEmpty Type = ""
)

// KnownTypes lists the handle prefixes this package can parse.
var KnownTypes = []Type{TypeDirV1, TypeUnified}

// Backing describes what a volume is physically made of on the Weka cluster. It is derived
// from the handle alone and requires no contact with the Weka API.
type Backing string

const (
	// BackingFilesystem is a whole Weka filesystem exposed as one volume.
	BackingFilesystem Backing = "filesystem"
	// BackingDirectory is a directory inside a Weka filesystem, usually quota-enforced.
	BackingDirectory Backing = "directory"
	// BackingSnapshot is a writable Weka snapshot of a filesystem.
	BackingSnapshot Backing = "snapshot"
	// BackingSnapshotDirectory is a directory inside a writable Weka snapshot.
	BackingSnapshotDirectory Backing = "snapshot-directory"
	// BackingUnknown is used when the handle could not be classified.
	BackingUnknown Backing = "unknown"
)

// ErrUnknownType indicates the handle does not begin with a recognised type prefix.
var ErrUnknownType = errors.New("volume handle does not start with a known type prefix")

// ErrEmptyFilesystem indicates the handle carries no filesystem name.
var ErrEmptyFilesystem = errors.New("volume handle contains no filesystem name")

// Handle is a parsed volume handle. The zero value is not useful; obtain one from Parse.
//
// The parsed fields are for inspection and classification only. To render the handle back
// to a string always use String, which returns the original bytes.
type Handle struct {
	// Type is the handle prefix, always one of KnownTypes.
	Type Type
	// FilesystemName is the Weka filesystem the volume lives on.
	FilesystemName string
	// SnapshotAccessPoint is the Weka snapshot access point, empty for non-snapshot volumes.
	SnapshotAccessPoint string
	// InnerPath is the path within the filesystem or snapshot, empty for whole-filesystem
	// volumes. It retains its leading separator(s) exactly as they appear in the handle,
	// which may legitimately be "//" — see the package documentation.
	InnerPath string

	// raw is the handle exactly as supplied to Parse.
	raw string
	// fsStart and fsEnd bound FilesystemName within raw, enabling a byte-exact splice.
	fsStart, fsEnd int
}

// Parse decomposes a volume handle. The returned Handle retains the input verbatim, so
// Parse followed by String is always an identity operation.
func Parse(handle string) (Handle, error) {
	if handle == "" {
		return Handle{}, fmt.Errorf("%w: handle is empty", ErrUnknownType)
	}

	var volType Type
	for _, candidate := range KnownTypes {
		if strings.HasPrefix(handle, string(candidate)+"/") {
			volType = candidate
			break
		}
	}
	if volType == "" {
		return Handle{}, fmt.Errorf("%w: %q", ErrUnknownType, handle)
	}

	// Everything after "<type>/" is "<fs>[:<accessPoint>][<innerPath>]", where innerPath
	// keeps whatever separators it was born with.
	bodyStart := len(volType) + 1
	body := handle[bodyStart:]

	fsAndAccessPoint := body
	innerPath := ""
	if idx := strings.Index(body, "/"); idx >= 0 {
		fsAndAccessPoint = body[:idx]
		innerPath = body[idx:]
	}

	filesystemName := fsAndAccessPoint
	accessPoint := ""
	if idx := strings.Index(fsAndAccessPoint, ":"); idx >= 0 {
		filesystemName = fsAndAccessPoint[:idx]
		accessPoint = fsAndAccessPoint[idx+1:]
	}
	if filesystemName == "" {
		return Handle{}, fmt.Errorf("%w: %q", ErrEmptyFilesystem, handle)
	}

	return Handle{
		Type:                volType,
		FilesystemName:      filesystemName,
		SnapshotAccessPoint: accessPoint,
		InnerPath:           innerPath,
		raw:                 handle,
		fsStart:             bodyStart,
		fsEnd:               bodyStart + len(filesystemName),
	}, nil
}

// String returns the handle exactly as it was parsed, including any non-normalised
// separators. It never re-assembles the handle from the parsed fields.
func (h Handle) String() string { return h.raw }

// Backing classifies the volume from its handle.
func (h Handle) Backing() Backing {
	switch {
	case h.raw == "":
		return BackingUnknown
	case h.SnapshotAccessPoint != "" && h.InnerPath != "":
		return BackingSnapshotDirectory
	case h.SnapshotAccessPoint != "":
		return BackingSnapshot
	case h.InnerPath != "":
		return BackingDirectory
	default:
		return BackingFilesystem
	}
}

// PortableAcrossWekaClusters reports whether the volume can be recreated against a
// different Weka cluster that received the data by replication.
//
// Weka does not replicate filesystem snapshots, so snapshot-backed volumes are portable
// only to another Kubernetes cluster attached to the same Weka cluster.
func (h Handle) PortableAcrossWekaClusters() bool {
	switch h.Backing() {
	case BackingFilesystem, BackingDirectory:
		return true
	default:
		return false
	}
}

// WithFilesystemName returns the handle with its filesystem name replaced, splicing the
// original bytes so that separators, access point and inner path survive untouched.
//
// This is the only supported way to retarget a handle at a renamed filesystem.
func (h Handle) WithFilesystemName(name string) (Handle, error) {
	if name == "" {
		return Handle{}, ErrEmptyFilesystem
	}
	if strings.ContainsAny(name, "/:") {
		return Handle{}, fmt.Errorf("filesystem name %q may not contain '/' or ':'", name)
	}
	return Parse(h.raw[:h.fsStart] + name + h.raw[h.fsEnd:])
}

// Build assembles a handle from components. It applies the same separator rules as the
// driver, which means an innerPath carrying a leading slash yields a doubled separator.
// Prefer Parse and WithFilesystemName when a handle already exists.
func Build(volType Type, filesystemName, snapshotAccessPoint, innerPath string) string {
	handle := string(volType) + "/" + filesystemName
	if snapshotAccessPoint != "" {
		handle += ":" + snapshotAccessPoint
	}
	if innerPath != "" {
		handle += "/" + innerPath
	}
	return handle
}

// SliceType returns the type prefix of a handle, mirroring the driver's historical
// tolerance for handles it cannot fully parse.
func SliceType(handle string) Type {
	parts := strings.Split(handle, "/")
	if len(parts) >= 2 {
		candidate := Type(strings.Join(parts[0:2], "/"))
		for _, known := range KnownTypes {
			if candidate == known {
				return known
			}
		}
	}
	if len(parts) > 1 && !strings.HasPrefix(parts[1], "v") {
		return TypeUnknown // not in "<name>/<version>" shape
	}
	if len(parts) == 1 {
		return TypeUnknown
	}
	if parts[0] == "" {
		return TypeUnknown // a Unix absolute path such as "/var/log/messages"
	}
	return TypeUnified
}

// SliceFilesystemName returns the filesystem name of a handle, or "" if absent.
func SliceFilesystemName(handle string) string {
	parts := strings.Split(handle, "/")
	if len(parts) < 3 {
		return ""
	}
	return strings.Split(parts[2], ":")[0]
}

// SliceSnapshotAccessPoint returns the snapshot access point of a handle, or "" if absent.
func SliceSnapshotAccessPoint(handle string) string {
	parts := strings.Split(handle, "/")
	if len(parts) < 3 {
		return ""
	}
	parts = strings.Split(parts[2], ":")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// SliceInnerPath returns the inner path of a handle including its leading separator(s),
// or "" if the handle addresses a whole filesystem or snapshot.
func SliceInnerPath(handle string) string {
	parts := strings.Split(handle, "/")
	if len(parts) <= 3 {
		return ""
	}
	return "/" + strings.Join(parts[3:], "/")
}
