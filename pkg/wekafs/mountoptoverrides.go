package wekafs

import (
	"context"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (

	// VolumeContextPodNameKey is the key in VolumeContext that describes the pod name
	VolumeContextPodNameKey = "csi.storage.k8s.io/pod.name"
	// VolumeContextPodNamespaceKey is the key in VolumeContext that describes the pod namespace
	VolumeContextPodNamespaceKey = "csi.storage.k8s.io/pod.namespace"
	// VolumeContextPvcNameKey is the key in VolumeContext that describes the PVC name
	VolumeContextPvcNameKey = "csi.storage.k8s.io/pvc/name"
	// VolumeContextPvcNamespaceKey is the key in VolumeContext that describes the PVC namespace
	VolumeContextPvcNamespaceKey = "csi.storage.k8s.io/pvc/namespace"

	// PodMountOptionOverrideAnnotation is the annotation key on pods for per-PVC mount option overrides.
	// Format: one entry per line (or separated by ';'):
	//   <pvc-name-regex>: <mount-option-modifiers>
	// Mount option modifiers are comma-separated with + (add) or - (remove) prefix
	// Example:
	//   my-volume-.*: -forcedirect, +readcache
	//   my-vol-1: -forcedirect, +readcache, +writecache
	//   my-vol-2: +inode_bits=64
	PodMountOptionOverrideAnnotation = "weka.io/mount-options-overrides"

	// PvcMountOptionOverrideAnnotation is the annotation key on PVCs for mount option overrides that apply to all pods mounting the PVC.
	// Format: same as PodMountOptionOverrideAnnotation but without the PVC name regex (applies to all pods).
	// Example:
	//   -forcedirect, +readcache
	PvcMountOptionOverrideAnnotation = "weka.io/mount-options-override"

	// Order of application:
	// 1. StorageClass default options
	// 2. Node Publish default options
	// 3. PvcMountOptionOverrideAnnotation
	// 4. PodMountOptionOverrideAnnotation (first matching pattern wins)
)

type podMountEntry struct {
	pattern  *regexp.Regexp
	override MountOptionOverride
}

// MountOptionOverride is a string that consists from mount options with optional override flag, separated by comma
// e.g. "+readcache,-forcedirect,inode_bits=64"
type MountOptionOverride string

func (mo MountOptionOverride) String() string {
	return string(mo)
}

// ApplyToOptions applies +/- prefixed mount option modifiers to opts
// and returns the resulting MountOptions.
//
// Prefix semantics:
//   - (or no prefix): add the option, respecting mutually exclusive sets
//     -: remove the option
func (mo MountOptionOverride) ApplyToOptions(opts MountOptions, exclusives []mutuallyExclusiveMountOptionSet) MountOptions {
	parts := strings.Split(string(mo), ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		switch {
		case strings.HasPrefix(part, "+"):
			toAdd := NewMountOptionsFromString(strings.TrimPrefix(part, "+"))
			opts.Merge(toAdd, exclusives)
		case strings.HasPrefix(part, "-"):
			opts = opts.RemoveOption(strings.TrimPrefix(part, "-"))
		default:
			toAdd := NewMountOptionsFromString(part)
			opts.Merge(toAdd, exclusives)
		}
	}
	return opts
}

// parsePodMountAnnotation parses the PodMountOptionOverrideAnnotation value into entries.
// Each entry maps a PVC name regex pattern to mount option modifiers.
func parsePodMountAnnotation(annotation string) []podMountEntry {
	var entries []podMountEntry
	lines := strings.FieldsFunc(annotation, func(r rune) bool {
		return r == ';' || r == '\n'
	})
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		pattern := strings.TrimSpace(line[:colonIdx])
		rawOpts := strings.TrimSpace(line[colonIdx+1:])
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			log.Warn().Str("pattern", pattern).Err(err).Msg("Invalid regex in weka.io/mount-options annotation, skipping entry")
			continue
		}
		entries = append(entries, podMountEntry{pattern: re, override: MountOptionOverride(rawOpts)})
	}
	return entries
}

// getPodMountOptionsOverride fetches the pod, reads the PodMountOptionOverrideAnnotation annotation,
// and returns the raw mount option modifiers for the first pattern that matches pvcClaimName.
// Returns "" if the annotation is absent or no pattern matches.
func getPodMountOptionsOverride(ctx context.Context, crclient runtimeclient.Reader, podNamespace, podName, pvcName string) MountOptionOverride {
	logger := log.Ctx(ctx)
	pod := &v1.Pod{}
	err := crclient.Get(ctx, types.NamespacedName{
		Namespace: podNamespace,
		Name:      podName,
	}, pod)
	if err != nil {
		logger.Warn().Err(err).
			Str("pod_namespace", podNamespace).
			Str("pod_name", podName).
			Msg("Failed to fetch pod for mount option annotation, skipping")
		return ""
	}
	annotation, ok := pod.Annotations[PodMountOptionOverrideAnnotation]
	if !ok || annotation == "" {
		return ""
	}

	for _, entry := range parsePodMountAnnotation(annotation) {
		if entry.pattern.MatchString(pvcName) {
			logger.Debug().
				Str("pvc_name", pvcName).
				Str("pattern", entry.pattern.String()).
				Str("opts", entry.override.String()).
				Msg("Matched pod annotation mount options for PVC")
			return entry.override
		}
	}
	return ""
}

// getPvcMountOptionsOverride fetches the PVC, reads the PvcMountOptionOverrideAnnotation annotation,
// and returns the raw mount option modifiers that apply to all pods mounting the PVC.
// Returns "" if the annotation is absent.
func getPvcMountOptionsOverride(ctx context.Context, crclient runtimeclient.Reader, pvcNamespace, pvcName string) MountOptionOverride {
	logger := log.Ctx(ctx)
	claim := &v1.PersistentVolumeClaim{}
	err := crclient.Get(ctx, types.NamespacedName{
		Namespace: pvcNamespace,
		Name:      pvcName,
	}, claim)
	if err != nil {
		logger.Warn().Err(err).
			Str("pvc_namespace", pvcNamespace).
			Str("pvc_name", pvcName).
			Msg("Failed to fetch PVC for mount option annotation, skipping")
		return ""
	}
	annotation, ok := claim.Annotations[PvcMountOptionOverrideAnnotation]
	if !ok || annotation == "" {
		return ""
	}
	return MountOptionOverride(annotation)
}
