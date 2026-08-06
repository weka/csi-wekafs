// Package collect discovers the Kubernetes objects that make up Weka CSI volumes and
// assembles them into an archive.
//
// Collection is read-only and never contacts the Weka cluster: an export is a snapshot of
// Kubernetes metadata, taken on the assumption that the data itself is safe on Weka.
package collect

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/rs/zerolog"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/wekafs/csi-wekafs/pkg/migrator/archive"
	"github.com/wekafs/csi-wekafs/pkg/migrator/convert"
	"github.com/wekafs/csi-wekafs/pkg/volumeid"
)

// DefaultDriverName matches the chart's csiDriverName default.
const DefaultDriverName = "csi.weka.io"

// secretRefParamPairs are the StorageClass parameters through which external-provisioner
// resolves CSI secrets. Both the name and namespace halves are needed to locate a secret.
var secretRefParamPairs = [][2]string{
	{"csi.storage.k8s.io/provisioner-secret-name", "csi.storage.k8s.io/provisioner-secret-namespace"},
	{"csi.storage.k8s.io/controller-publish-secret-name", "csi.storage.k8s.io/controller-publish-secret-namespace"},
	{"csi.storage.k8s.io/node-stage-secret-name", "csi.storage.k8s.io/node-stage-secret-namespace"},
	{"csi.storage.k8s.io/node-publish-secret-name", "csi.storage.k8s.io/node-publish-secret-namespace"},
	{"csi.storage.k8s.io/controller-expand-secret-name", "csi.storage.k8s.io/controller-expand-secret-namespace"},
	{"csi.storage.k8s.io/node-expand-secret-name", "csi.storage.k8s.io/node-expand-secret-namespace"},
}

// Options controls what an export includes.
type Options struct {
	// DriverName identifies volumes to export. The chart allows overriding it, so it is a
	// flag rather than a constant.
	DriverName string
	// Namespace restricts the export to claims in one namespace. Secrets and
	// StorageClasses referenced from there are still collected wherever they live.
	Namespace string
	// IncludeSecretData exports credentials verbatim instead of redacting them.
	IncludeSecretData bool
	// SkipUnexportable drops volumes that cannot be recreated against a different Weka
	// cluster, instead of exporting them with a warning.
	SkipUnexportable bool
	// Tool is recorded in the manifest for support.
	Tool string
}

type secretRef struct{ namespace, name string }

// Collector reads one cluster.
type Collector struct {
	client kubernetes.Interface
	opts   Options
}

// New returns a Collector reading through client.
func New(client kubernetes.Interface, opts Options) *Collector {
	if opts.DriverName == "" {
		opts.DriverName = DefaultDriverName
	}
	return &Collector{client: client, opts: opts}
}

// Collect gathers every object needed to recreate the driver's volumes elsewhere.
func (c *Collector) Collect(ctx context.Context) (*archive.Writer, error) {
	logger := zerolog.Ctx(ctx)

	w := archive.NewWriter(c.opts.Tool, c.opts.DriverName)
	w.SetNamespace(c.opts.Namespace)
	w.SetSource(c.describeSource(ctx))

	pvs, err := c.wekaVolumes(ctx)
	if err != nil {
		return nil, err
	}
	if len(pvs) == 0 {
		w.AddWarning("no PersistentVolumes provisioned by driver %q were found", c.opts.DriverName)
	}
	logger.Info().Int("count", len(pvs)).Msg("Found PersistentVolumes provisioned by the driver")

	// Claims are indexed up front rather than fetched per volume. A Get per PersistentVolume
	// would make export time scale with cluster size: at a thousand volumes that is a
	// thousand sequential round trips, where one List costs a single one.
	claims, err := c.claimIndex(ctx)
	if err != nil {
		return nil, err
	}

	storageClasses := map[string]struct{}{}
	secrets := map[secretRef]struct{}{}
	claimsByClass := map[string][]*corev1.PersistentVolumeClaim{}

	for i := range pvs {
		pv := &pvs[i]

		handle, handleErr := volumeid.Parse(pv.Spec.CSI.VolumeHandle)
		if handleErr != nil {
			// An unparseable handle is exported verbatim: the driver, not this tool, is the
			// authority on what it can mount. Flag it so the operator can look.
			w.AddWarning("PersistentVolume %q has a volume handle this tool does not recognise (%v); it was exported unchanged", pv.Name, handleErr)
		} else if !handle.PortableAcrossWekaClusters() {
			if c.opts.SkipUnexportable {
				w.AddWarning("skipped PersistentVolume %q: %s-backed volumes cannot be recreated against a different Weka cluster", pv.Name, handle.Backing())
				continue
			}
			w.AddWarning("PersistentVolume %q is %s-backed: Weka cannot replicate snapshots, so this volume can only be restored to a Kubernetes cluster attached to the same Weka cluster", pv.Name, handle.Backing())
		}

		// The claim's namespace is on the volume itself, so a namespace-scoped export can
		// discard foreign volumes without consulting the claim at all.
		if c.opts.Namespace != "" && (pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.Namespace != c.opts.Namespace) {
			continue
		}
		claim := claims.lookup(pv)

		logger.Debug().
			Str("pv", pv.Name).
			Str("volume_handle", pv.Spec.CSI.VolumeHandle).
			Str("backing", string(handle.Backing())).
			Str("claim", describeClaim(claim)).
			Msg("Exporting volume")

		if err := c.addPV(w, pv, handle, handleErr == nil, claim); err != nil {
			return nil, err
		}
		if claim != nil {
			if err := c.addPVC(w, claim, pv.Name); err != nil {
				return nil, err
			}
			if class := claimStorageClass(claim); class != "" {
				claimsByClass[class] = append(claimsByClass[class], claim)
			}
		}

		if pv.Spec.StorageClassName != "" {
			storageClasses[pv.Spec.StorageClassName] = struct{}{}
		}
		if claim != nil {
			if class := claimStorageClass(claim); class != "" {
				storageClasses[class] = struct{}{}
			}
		}
		for _, ref := range pvSecretRefs(pv) {
			secrets[ref] = struct{}{}
		}
	}

	if err := c.addStorageClasses(ctx, w, storageClasses, claimsByClass, secrets); err != nil {
		return nil, err
	}
	if err := c.addSecrets(ctx, w, secrets); err != nil {
		return nil, err
	}
	logger.Debug().
		Int("storage_classes", len(storageClasses)).
		Int("secrets", len(secrets)).
		Msg("Collected referenced StorageClasses and Secrets")
	return w, nil
}

func describeClaim(claim *corev1.PersistentVolumeClaim) string {
	if claim == nil {
		return "<unbound>"
	}
	return claim.Namespace + "/" + claim.Name
}

// wekaVolumes lists PersistentVolumes provisioned by the configured driver.
func (c *Collector) wekaVolumes(ctx context.Context) ([]corev1.PersistentVolume, error) {
	list, err := c.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing PersistentVolumes: %w", err)
	}
	var out []corev1.PersistentVolume
	for _, pv := range list.Items {
		if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == c.opts.DriverName {
			out = append(out, pv)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// claimIndex is every claim in scope, keyed by namespace and name.
type claimIndex map[string]*corev1.PersistentVolumeClaim

// claimIndex lists the claims once. A namespace-scoped export lists only that namespace,
// since no volume outside it will be exported anyway.
func (c *Collector) claimIndex(ctx context.Context) (claimIndex, error) {
	list, err := c.client.CoreV1().PersistentVolumeClaims(c.opts.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing PersistentVolumeClaims: %w", err)
	}
	index := make(claimIndex, len(list.Items))
	for i := range list.Items {
		claim := &list.Items[i]
		index[claim.Namespace+"/"+claim.Name] = claim
	}
	return index, nil
}

// lookup returns the claim a volume is bound to, or nil. A dangling claimRef is tolerated so
// that a Released volume can still be exported.
func (i claimIndex) lookup(pv *corev1.PersistentVolume) *corev1.PersistentVolumeClaim {
	if pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.Name == "" {
		return nil
	}
	return i[pv.Spec.ClaimRef.Namespace+"/"+pv.Spec.ClaimRef.Name]
}

func (c *Collector) addPV(w *archive.Writer, pv *corev1.PersistentVolume, handle volumeid.Handle, handleParsed bool, claim *corev1.PersistentVolumeClaim) error {
	u, err := convert.ToUnstructured(pv.DeepCopy(), "v1", "PersistentVolume")
	if err != nil {
		return err
	}
	if err := convert.StaticPV(u); err != nil {
		return err
	}
	doc, err := marshal(u)
	if err != nil {
		return err
	}

	record := archive.VolumeRecord{
		PVName:       pv.Name,
		StorageClass: pv.Spec.StorageClassName,
		VolumeHandle: pv.Spec.CSI.VolumeHandle,
		Capacity:     pv.Spec.Capacity.Storage().String(),
	}
	if handleParsed {
		record.FilesystemName = handle.FilesystemName
		record.Backing = string(handle.Backing())
		record.PortableAcrossWekaClusters = handle.PortableAcrossWekaClusters()
	} else {
		record.Backing = string(volumeid.BackingUnknown)
	}
	if claim != nil {
		record.PVCNamespace, record.PVCName = claim.Namespace, claim.Name
	}
	w.AddVolumeRecord(record)

	return w.Add(path("persistentvolumes", "", pv.Name), "v1", "PersistentVolume", "", pv.Name, doc)
}

func (c *Collector) addPVC(w *archive.Writer, claim *corev1.PersistentVolumeClaim, pvName string) error {
	u, err := convert.ToUnstructured(claim.DeepCopy(), "v1", "PersistentVolumeClaim")
	if err != nil {
		return err
	}
	if err := convert.StaticPVC(u, pvName); err != nil {
		return err
	}
	doc, err := marshal(u)
	if err != nil {
		return err
	}
	return w.Add(path("persistentvolumeclaims", claim.Namespace, claim.Name),
		"v1", "PersistentVolumeClaim", claim.Namespace, claim.Name, doc)
}

func (c *Collector) addStorageClasses(ctx context.Context, w *archive.Writer, names map[string]struct{}, claimsByClass map[string][]*corev1.PersistentVolumeClaim, secrets map[secretRef]struct{}) error {
	for _, name := range sortedKeys(names) {
		sc, err := c.client.StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			w.AddWarning("StorageClass %q is referenced by an exported object but no longer exists; recreate it manually before importing", name)
			continue
		}
		if err != nil {
			return fmt.Errorf("getting StorageClass %s: %w", name, err)
		}

		for _, ref := range c.storageClassSecretRefs(w, sc, claimsByClass[name]) {
			secrets[ref] = struct{}{}
		}

		u, err := convert.ToUnstructured(sc.DeepCopy(), "storage.k8s.io/v1", "StorageClass")
		if err != nil {
			return err
		}
		convert.NeatStorageClass(u)
		doc, err := marshal(u)
		if err != nil {
			return err
		}
		if err := w.Add(path("storageclasses", "", sc.Name), "storage.k8s.io/v1", "StorageClass", "", sc.Name, doc); err != nil {
			return err
		}
	}
	return nil
}

// storageClassSecretRefs resolves the secret references a StorageClass declares, expanding
// the ${pvc.namespace} and ${pvc.name} templates against each claim that uses the class.
func (c *Collector) storageClassSecretRefs(w *archive.Writer, sc *storagev1.StorageClass, claims []*corev1.PersistentVolumeClaim) []secretRef {
	var refs []secretRef
	for _, pair := range secretRefParamPairs {
		nameTemplate, ok := sc.Parameters[pair[0]]
		if !ok || nameTemplate == "" {
			continue
		}
		namespaceTemplate := sc.Parameters[pair[1]]
		if namespaceTemplate == "" {
			// Without a namespace parameter the reference cannot be resolved, and
			// external-provisioner would reject it too.
			w.AddWarning("StorageClass %q sets %s without %s; the secret could not be resolved", sc.Name, pair[0], pair[1])
			continue
		}

		if !isTemplated(nameTemplate) && !isTemplated(namespaceTemplate) {
			refs = append(refs, secretRef{namespace: namespaceTemplate, name: nameTemplate})
			continue
		}
		if len(claims) == 0 {
			w.AddWarning("StorageClass %q references a templated secret (%s=%q) but no exported claim uses the class, so it could not be resolved", sc.Name, pair[0], nameTemplate)
			continue
		}
		for _, claim := range claims {
			name, nameOK := expand(nameTemplate, claim)
			namespace, namespaceOK := expand(namespaceTemplate, claim)
			if !nameOK || !namespaceOK {
				w.AddWarning("StorageClass %q references secret template %q/%q which uses a substitution this tool cannot resolve; export the secret manually", sc.Name, namespaceTemplate, nameTemplate)
				continue
			}
			refs = append(refs, secretRef{namespace: namespace, name: name})
		}
	}
	return refs
}

func (c *Collector) addSecrets(ctx context.Context, w *archive.Writer, refs map[secretRef]struct{}) error {
	redactedByPath := map[string][]string{}

	ordered := make([]secretRef, 0, len(refs))
	for ref := range refs {
		ordered = append(ordered, ref)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].namespace != ordered[j].namespace {
			return ordered[i].namespace < ordered[j].namespace
		}
		return ordered[i].name < ordered[j].name
	})

	for _, ref := range ordered {
		secret, err := c.client.CoreV1().Secrets(ref.namespace).Get(ctx, ref.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			w.AddWarning("Secret %s/%s is referenced by an exported object but does not exist", ref.namespace, ref.name)
			continue
		}
		if err != nil {
			return fmt.Errorf("getting Secret %s/%s: %w", ref.namespace, ref.name, err)
		}

		u, err := convert.ToUnstructured(secret.DeepCopy(), "v1", "Secret")
		if err != nil {
			return err
		}
		convert.NeatSecret(u)

		objectPath := path("secrets", secret.Namespace, secret.Name)
		if !c.opts.IncludeSecretData {
			redacted, err := convert.RedactSecret(u)
			if err != nil {
				return err
			}
			if len(redacted) > 0 {
				redactedByPath[objectPath] = redacted
			}
		}

		doc, err := marshal(u)
		if err != nil {
			return err
		}
		if err := w.Add(objectPath, "v1", "Secret", secret.Namespace, secret.Name, doc); err != nil {
			return err
		}
	}

	w.SetSecretDisposition(c.opts.IncludeSecretData, redactedByPath)
	if !c.opts.IncludeSecretData && len(redactedByPath) > 0 {
		w.AddWarning("credentials were redacted from %d secret(s); re-export with --include-secret-data to make the archive directly importable", len(redactedByPath))
	}
	return nil
}

// describeSource records where the export came from. Failures are non-fatal: this is
// provenance for a human reader, not something correctness depends on.
func (c *Collector) describeSource(ctx context.Context) archive.SourceCluster {
	var src archive.SourceCluster
	if ns, err := c.client.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{}); err == nil {
		src.KubeSystemUID = string(ns.UID)
	}
	if version, err := c.client.Discovery().ServerVersion(); err == nil {
		src.ServerVersion = version.GitVersion
	}
	return src
}

// pvSecretRefs returns the secrets a PersistentVolume references directly.
func pvSecretRefs(pv *corev1.PersistentVolume) []secretRef {
	var refs []secretRef
	add := func(ref *corev1.SecretReference) {
		if ref != nil && ref.Name != "" && ref.Namespace != "" {
			refs = append(refs, secretRef{namespace: ref.Namespace, name: ref.Name})
		}
	}
	add(pv.Spec.CSI.ControllerPublishSecretRef)
	add(pv.Spec.CSI.NodeStageSecretRef)
	add(pv.Spec.CSI.NodePublishSecretRef)
	add(pv.Spec.CSI.ControllerExpandSecretRef)
	add(pv.Spec.CSI.NodeExpandSecretRef)
	return refs
}

func claimStorageClass(claim *corev1.PersistentVolumeClaim) string {
	if claim.Spec.StorageClassName == nil {
		return ""
	}
	return *claim.Spec.StorageClassName
}

func isTemplated(s string) bool { return strings.Contains(s, "${") }

// expand resolves the two substitutions external-provisioner supports that this tool can
// evaluate offline. Anything else is reported rather than guessed at.
func expand(template string, claim *corev1.PersistentVolumeClaim) (string, bool) {
	out := strings.ReplaceAll(template, "${pvc.namespace}", claim.Namespace)
	out = strings.ReplaceAll(out, "${pvc.name}", claim.Name)
	if isTemplated(out) {
		return "", false
	}
	return out, true
}

// path builds the archive-relative location of an object.
func path(kindDir, namespace, name string) string {
	if namespace == "" {
		return fmt.Sprintf("objects/%s/%s.yaml", kindDir, name)
	}
	return fmt.Sprintf("objects/%s/%s/%s.yaml", kindDir, namespace, name)
}

func marshal(u *unstructured.Unstructured) ([]byte, error) {
	doc, err := yaml.Marshal(u.Object)
	if err != nil {
		return nil, fmt.Errorf("encoding %s %s/%s: %w", u.GetKind(), u.GetNamespace(), u.GetName(), err)
	}
	return doc, nil
}

func sortedKeys(m map[string]struct{}) []string {
	return slices.Sorted(maps.Keys(m))
}
