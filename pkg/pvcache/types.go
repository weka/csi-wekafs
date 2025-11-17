package pvcache

import (
	"time"

	v1 "k8s.io/api/core/v1"
)

// VolumeBackingType represents the type of backing storage for a PersistentVolume
type VolumeBackingType string

const (
	VolumeBackingDirectory VolumeBackingType = "directory"

	VolumeBackingFilesystem VolumeBackingType = "filesystem"

	VolumeBackingSnapshot VolumeBackingType = "snapshot"
)

// pvCacheEntry represents a cached PersistentVolume with parsed metadata.
type pvCacheEntry struct {
	// pv is the Kubernetes PersistentVolume object
	pv *v1.PersistentVolume

	// filesystemName is the parsed filesystem name from the volumeHandle
	filesystemName string

	// capacity is the storage capacity in bytes
	capacity int64

	// backingType indicates how the volume is backed (directory, filesystem, or snapshot)
	backingType VolumeBackingType
}

// Config contains configuration options for the PV cache.
type Config struct {
	// FetchInterval is how often to fetch PVs from Kubernetes API.
	// Default: 60 seconds
	FetchInterval time.Duration

	// ChannelBufferSize is the buffer size for the PV processing channel.
	// Default: 10000
	ChannelBufferSize int
}
