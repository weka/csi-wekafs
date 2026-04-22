package wekafs

import (
	"context"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// PodMountOptionsAnnotation is the annotation key on pods for per-PVC mount option overrides.
	// Format: one entry per line (or separated by ';'):
	//   <pvc-name-regex>: <mount-option-modifiers>
	// Mount option modifiers are comma-separated with + (add) or - (remove) prefix.
	// Example:
	//   my-volume-.*: -forcedirect, +readcache
	//   my-vol-1: -forcedirect, +readcache, +writecache
	//   my-vol-2: +inode_bits=64
	PodMountOptionsAnnotation = "weka.io/mount-options"

	// Standard CSI context keys injected by Kubernetes when podInfoOnMount: true
	CSIPodNameKey      = "csi.storage.k8s.io/pod.name"
	CSIPodNamespaceKey = "csi.storage.k8s.io/pod.namespace"
	CSIPodUIDKey       = "csi.storage.k8s.io/pod.uid"
)

type podMountEntry struct {
	pattern *regexp.Regexp
	rawOpts string
}

// parsePodMountAnnotation parses the weka.io/mount-options annotation value into entries.
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
		entries = append(entries, podMountEntry{pattern: re, rawOpts: rawOpts})
	}
	return entries
}

// applyAnnotationMountOptions applies +/- prefixed mount option modifiers to opts
// and returns the resulting MountOptions.
//
// Prefix semantics:
//   + (or no prefix): add the option, respecting mutually exclusive sets
//   -: remove the option
func applyAnnotationMountOptions(opts MountOptions, rawOpts string, exclusives []mutuallyExclusiveMountOptionSet) MountOptions {
	parts := strings.Split(rawOpts, ",")
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

// extractPVNameFromTargetPath parses the PV name out of a kubelet CSI target path.
// Expected format: .../volumes/kubernetes.io~csi/<pvName>/mount
func extractPVNameFromTargetPath(targetPath string) string {
	parts := strings.Split(targetPath, "/")
	for i, part := range parts {
		if part == "kubernetes.io~csi" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// getPVCClaimNameFromTargetPath extracts the PV name from the target path and
// returns the PVC claim name bound to that PV.
func getPVCClaimNameFromTargetPath(ctx context.Context, kubeClient kubernetes.Interface, targetPath string) (string, error) {
	pvName := extractPVNameFromTargetPath(targetPath)
	if pvName == "" {
		return "", nil
	}
	pv, err := kubeClient.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if pv.Spec.ClaimRef == nil {
		return "", nil
	}
	return pv.Spec.ClaimRef.Name, nil
}

// getPodAnnotationMountOptions fetches the pod, reads the weka.io/mount-options annotation,
// and returns the raw mount option modifiers for the first pattern that matches pvcClaimName.
// Returns "" if the annotation is absent or no pattern matches.
func getPodAnnotationMountOptions(ctx context.Context, kubeClient kubernetes.Interface, podNamespace, podName, pvcClaimName string) string {
	logger := log.Ctx(ctx)
	pod, err := kubeClient.CoreV1().Pods(podNamespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		logger.Warn().Err(err).
			Str("pod_namespace", podNamespace).
			Str("pod_name", podName).
			Msg("Failed to fetch pod for mount option annotation, skipping")
		return ""
	}
	annotation, ok := pod.Annotations[PodMountOptionsAnnotation]
	if !ok || annotation == "" {
		return ""
	}
	for _, entry := range parsePodMountAnnotation(annotation) {
		if entry.pattern.MatchString(pvcClaimName) {
			logger.Debug().
				Str("pvc_claim_name", pvcClaimName).
				Str("pattern", entry.pattern.String()).
				Str("opts", entry.rawOpts).
				Msg("Matched pod annotation mount options for PVC")
			return entry.rawOpts
		}
	}
	return ""
}
