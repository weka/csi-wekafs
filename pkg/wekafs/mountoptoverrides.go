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
			opts = addOverride(opts, strings.TrimPrefix(part, "+"), exclusives)
		case strings.HasPrefix(part, "-"):
			// ExcludeOption rather than RemoveOption, so the removal also survives the
			// defaults that are merged underneath these options at mount time.
			opts = opts.ExcludeOption(strings.TrimPrefix(part, "-"))
		default:
			opts = addOverride(opts, part, exclusives)
		}
	}
	return opts
}

// addOverride adds an option, first clearing any exclusion an earlier "-opt" recorded for it,
// so that "-opt,+opt" ends up adding the option rather than being cancelled at merge time.
func addOverride(opts MountOptions, optstring string, exclusives []mutuallyExclusiveMountOptionSet) MountOptions {
	opts = opts.UnexcludeOption(optstring)
	opts.Merge(NewMountOptionsFromString(optstring), exclusives)
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
