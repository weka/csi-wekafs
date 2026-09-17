package transform

import (
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/wekafs/csi-wekafs/pkg/migrator/convert"
	"github.com/wekafs/csi-wekafs/pkg/volumeid"
)

// secretRefFields are the PersistentVolume fields that reference a CSI secret.
var secretRefFields = []string{
	"controllerPublishSecretRef",
	"nodeStageSecretRef",
	"nodePublishSecretRef",
	"controllerExpandSecretRef",
	"nodeExpandSecretRef",
}

// NewChainFromConfig builds the rules a config calls for, omitting those it does not
// configure. Rule order does not affect the result; see the package documentation.
func NewChainFromConfig(cfg *Config) (*Chain, error) {
	if cfg == nil {
		return NewChain(), nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	rec := newRecorder()
	var rules []Rule

	if cfg.TargetNamespace != "" || len(cfg.Namespaces) > 0 {
		rules = append(rules, &namespaceRule{cfg: cfg, rec: rec})
	}
	if len(cfg.Filesystems) > 0 {
		rules = append(rules, &filesystemRule{simpleMapping{field: "filesystems", mapping: cfg.Filesystems, rec: rec}})
	}
	if cfg.DriverName != "" {
		rules = append(rules, &driverRule{target: cfg.DriverName, rec: rec})
	}
	if len(cfg.StorageClasses) > 0 {
		rules = append(rules, &storageClassRule{simpleMapping{field: "storageClasses", mapping: cfg.StorageClasses, rec: rec}})
	}
	if len(cfg.PersistentVolumes) > 0 {
		rules = append(rules, &pvRenameRule{simpleMapping{field: "persistentVolumes", mapping: cfg.PersistentVolumes, rec: rec}})
	}
	if len(cfg.PersistentVolumeClaims) > 0 {
		rules = append(rules, &pvcRenameRule{mapping: cfg.PersistentVolumeClaims, rec: rec})
	}
	if len(cfg.Secrets) > 0 {
		rules = append(rules, &secretRule{overrides: cfg.Secrets, rec: rec})
	}
	if cfg.MountOptions != nil {
		rules = append(rules, &mountOptionsRule{spec: cfg.MountOptions, rec: rec})
	}
	if cfg.NodeAffinity != nil {
		rules = append(rules, &nodeAffinityRule{spec: cfg.NodeAffinity, rec: rec})
	}
	if cfg.Metadata != nil {
		rules = append(rules, &metadataRule{spec: cfg.Metadata, rec: rec})
	}

	return &Chain{rules: rules, rec: rec, declared: cfg.declaredKeys()}, nil
}

// simpleMapping is a source-to-target rename declared in the config. The three rules that
// rename by a bare name share it; rules keying on a composite identity or on more than a
// name keep their own lookup, because forcing them into this shape would hide what they do.
type simpleMapping struct {
	// field is the config key, used to report a mapping that matched nothing.
	field   string
	mapping map[string]string
	rec     *recorder
}

// target resolves a source name, reporting whether it actually changes.
func (m simpleMapping) target(source string) (string, bool) {
	mapped, ok := m.mapping[source]
	if !ok || mapped == source {
		return source, false
	}
	m.rec.use(m.field + "[" + source + "]")
	return mapped, true
}

// describe names an object for change output.
func describe(u *unstructured.Unstructured) string {
	if u.GetNamespace() == "" {
		return u.GetKind() + "/" + u.GetName()
	}
	return u.GetKind() + "/" + u.GetNamespace() + "/" + u.GetName()
}

// --- namespace ------------------------------------------------------------------------

type namespaceRule struct {
	cfg *Config
	rec *recorder
}

func (r *namespaceRule) Name() string { return "namespace" }

// target resolves a source namespace, reporting whether it changes.
func (r *namespaceRule) target(source string) (string, bool) {
	if r.cfg.TargetNamespace != "" {
		return r.cfg.TargetNamespace, r.cfg.TargetNamespace != source
	}
	mapped, ok := r.cfg.Namespaces[source]
	if !ok {
		return source, false
	}
	r.rec.use("namespaces[" + source + "]")
	return mapped, mapped != source
}

func (r *namespaceRule) Apply(obj, original *unstructured.Unstructured) error {
	switch original.GetKind() {
	case "PersistentVolumeClaim":
		source := original.GetNamespace()
		if mapped, changed := r.target(source); changed {
			obj.SetNamespace(mapped)
			r.rec.record(r.Name(), describe(original), "metadata.namespace", source, mapped)
		}

	case "PersistentVolume":
		// The claimRef must follow its claim, or the restored volume binds to nothing.
		source, found, err := unstructured.NestedString(original.Object, "spec", "claimRef", "namespace")
		if err != nil || !found || source == "" {
			return nil
		}
		if mapped, changed := r.target(source); changed {
			if err := unstructured.SetNestedField(obj.Object, mapped, "spec", "claimRef", "namespace"); err != nil {
				return err
			}
			r.rec.record(r.Name(), describe(original), "spec.claimRef.namespace", source, mapped)
		}
	}
	return nil
}

// --- filesystem -----------------------------------------------------------------------

type filesystemRule struct {
	simpleMapping
}

func (r *filesystemRule) Name() string { return "filesystem" }

func (r *filesystemRule) Apply(obj, original *unstructured.Unstructured) error {
	switch original.GetKind() {
	case "PersistentVolume":
		return r.applyToVolume(obj, original)
	case "StorageClass":
		source, found, err := unstructured.NestedString(original.Object, "parameters", "filesystemName")
		if err != nil || !found {
			return nil
		}
		if mapped, changed := r.target(source); changed {
			if err := unstructured.SetNestedField(obj.Object, mapped, "parameters", "filesystemName"); err != nil {
				return err
			}
			r.rec.record(r.Name(), describe(original), "parameters.filesystemName", source, mapped)
		}
	}
	return nil
}

func (r *filesystemRule) applyToVolume(obj, original *unstructured.Unstructured) error {
	raw, found, err := unstructured.NestedString(original.Object, "spec", "csi", "volumeHandle")
	if err != nil || !found || raw == "" {
		return nil
	}
	handle, err := volumeid.Parse(raw)
	if err != nil {
		// An unrecognised handle is left alone rather than guessed at. The export already
		// warned about it, and rewriting it blindly could point the volume anywhere.
		return nil
	}

	mapped, changed := r.target(handle.FilesystemName)
	if !changed {
		return nil
	}

	// Splice rather than reassemble: the handle is opaque and must survive byte-for-byte
	// apart from the filesystem name. See pkg/volumeid.
	renamed, err := handle.WithFilesystemName(mapped)
	if err != nil {
		return fmt.Errorf("rewriting volume handle of %s: %w", describe(original), err)
	}
	if err := unstructured.SetNestedField(obj.Object, renamed.String(), "spec", "csi", "volumeHandle"); err != nil {
		return err
	}
	r.rec.record(r.Name(), describe(original), "spec.csi.volumeHandle", raw, renamed.String())

	// The volume attribute must agree with the handle, or the object contradicts itself.
	attr, found, err := unstructured.NestedString(original.Object, "spec", "csi", "volumeAttributes", "filesystemName")
	if err == nil && found && attr == handle.FilesystemName {
		if err := unstructured.SetNestedField(obj.Object, mapped, "spec", "csi", "volumeAttributes", "filesystemName"); err != nil {
			return err
		}
		r.rec.record(r.Name(), describe(original), "spec.csi.volumeAttributes.filesystemName", attr, mapped)
	}
	return nil
}

// --- driver name --------------------------------------------------------------------

type driverRule struct {
	target string
	rec    *recorder
}

func (r *driverRule) Name() string { return "driverName" }

// Apply retargets the CSI driver on volumes and the provisioner on classes.
//
// Both must move together. A PersistentVolume naming a driver the target cluster does not
// have stays Pending forever with no node able to stage it, and a StorageClass whose
// provisioner disagrees with its volumes silently stops serving new claims.
func (r *driverRule) Apply(obj, original *unstructured.Unstructured) error {
	var field []string
	switch original.GetKind() {
	case "PersistentVolume":
		field = []string{"spec", "csi", "driver"}
	case "StorageClass":
		field = []string{"provisioner"}
	default:
		return nil
	}

	source, found, err := unstructured.NestedString(original.Object, field...)
	if err != nil || !found || source == "" {
		return nil
	}
	// Mark used as soon as a driver-bearing object is seen, so the unused-mapping report
	// only fires when the archive genuinely had nothing to retarget.
	r.rec.use("driverName")
	if source == r.target {
		return nil
	}
	if err := unstructured.SetNestedField(obj.Object, r.target, field...); err != nil {
		return err
	}
	r.rec.record(r.Name(), describe(original), strings.Join(field, "."), source, r.target)
	return nil
}

// --- storage class --------------------------------------------------------------------

type storageClassRule struct {
	simpleMapping
}

func (r *storageClassRule) Name() string { return "storageClass" }

func (r *storageClassRule) Apply(obj, original *unstructured.Unstructured) error {
	switch original.GetKind() {
	case "StorageClass":
		if mapped, changed := r.target(original.GetName()); changed {
			obj.SetName(mapped)
			r.rec.record(r.Name(), describe(original), "metadata.name", original.GetName(), mapped)
		}

	case "PersistentVolume", "PersistentVolumeClaim":
		// Volumes and claims must move together, or the claim stops matching its volume.
		source, found, err := unstructured.NestedString(original.Object, "spec", "storageClassName")
		if err != nil || !found || source == "" {
			return nil
		}
		if mapped, changed := r.target(source); changed {
			if err := unstructured.SetNestedField(obj.Object, mapped, "spec", "storageClassName"); err != nil {
				return err
			}
			r.rec.record(r.Name(), describe(original), "spec.storageClassName", source, mapped)
		}
	}
	return nil
}

// --- PersistentVolume rename ------------------------------------------------------------

type pvRenameRule struct {
	simpleMapping
}

func (r *pvRenameRule) Name() string { return "persistentVolume" }

func (r *pvRenameRule) Apply(obj, original *unstructured.Unstructured) error {
	switch original.GetKind() {
	case "PersistentVolume":
		if mapped, changed := r.target(original.GetName()); changed {
			obj.SetName(mapped)
			r.rec.record(r.Name(), describe(original), "metadata.name", original.GetName(), mapped)
		}

	case "PersistentVolumeClaim":
		// spec.volumeName is what makes the claim static; a stale value would leave it
		// waiting for a volume that no longer exists under that name.
		source, found, err := unstructured.NestedString(original.Object, "spec", "volumeName")
		if err != nil || !found || source == "" {
			return nil
		}
		if mapped, changed := r.target(source); changed {
			if err := unstructured.SetNestedField(obj.Object, mapped, "spec", "volumeName"); err != nil {
				return err
			}
			r.rec.record(r.Name(), describe(original), "spec.volumeName", source, mapped)
		}
	}
	return nil
}

// --- PersistentVolumeClaim rename --------------------------------------------------------

type pvcRenameRule struct {
	mapping map[string]string
	rec     *recorder
}

func (r *pvcRenameRule) Name() string { return "persistentVolumeClaim" }

// target resolves a claim identified by its source namespace and name.
func (r *pvcRenameRule) target(namespace, name string) (string, bool) {
	key := namespace + "/" + name
	mapped, ok := r.mapping[key]
	if !ok || mapped == name {
		return name, false
	}
	r.rec.use("persistentVolumeClaims[" + key + "]")
	return mapped, true
}

func (r *pvcRenameRule) Apply(obj, original *unstructured.Unstructured) error {
	switch original.GetKind() {
	case "PersistentVolumeClaim":
		if mapped, changed := r.target(original.GetNamespace(), original.GetName()); changed {
			obj.SetName(mapped)
			r.rec.record(r.Name(), describe(original), "metadata.name", original.GetName(), mapped)
		}

	case "PersistentVolume":
		namespace, _, err := unstructured.NestedString(original.Object, "spec", "claimRef", "namespace")
		if err != nil {
			return nil
		}
		name, found, err := unstructured.NestedString(original.Object, "spec", "claimRef", "name")
		if err != nil || !found || name == "" {
			return nil
		}
		if mapped, changed := r.target(namespace, name); changed {
			if err := unstructured.SetNestedField(obj.Object, mapped, "spec", "claimRef", "name"); err != nil {
				return err
			}
			r.rec.record(r.Name(), describe(original), "spec.claimRef.name", name, mapped)
		}
	}
	return nil
}

// --- secrets ------------------------------------------------------------------------------

type secretRule struct {
	overrides map[string]SecretOverride
	rec       *recorder
}

func (r *secretRule) Name() string { return "secret" }

// lookup finds the override for a secret reference and reports its new identity.
func (r *secretRule) lookup(namespace, name string) (SecretOverride, string, string, bool) {
	key := namespace + "/" + name
	override, ok := r.overrides[key]
	if !ok {
		return SecretOverride{}, "", "", false
	}
	r.rec.use("secrets[" + key + "]")

	newName, newNamespace := name, namespace
	if override.Name != "" {
		newName = override.Name
	}
	if override.Namespace != "" {
		newNamespace = override.Namespace
	}
	return override, newNamespace, newName, true
}

func (r *secretRule) Apply(obj, original *unstructured.Unstructured) error {
	switch original.GetKind() {
	case "Secret":
		return r.applyToSecret(obj, original)
	case "PersistentVolume":
		return r.applyToVolumeRefs(obj, original)
	case "StorageClass":
		return r.applyToClassParams(obj, original)
	}
	return nil
}

func (r *secretRule) applyToSecret(obj, original *unstructured.Unstructured) error {
	override, namespace, name, ok := r.lookup(original.GetNamespace(), original.GetName())
	if !ok {
		return nil
	}

	if name != original.GetName() {
		obj.SetName(name)
		r.rec.record(r.Name(), describe(original), "metadata.name", original.GetName(), name)
	}
	if namespace != original.GetNamespace() {
		obj.SetNamespace(namespace)
		r.rec.record(r.Name(), describe(original), "metadata.namespace", original.GetNamespace(), namespace)
	}

	if len(override.Data) == 0 && len(override.RemoveData) == 0 {
		return nil
	}

	data, _, err := unstructured.NestedMap(obj.Object, "data")
	if err != nil {
		return fmt.Errorf("reading data of %s: %w", describe(original), err)
	}
	if data == nil {
		data = map[string]any{}
	}

	// Sorted so that the recorded changes are deterministic.
	for _, key := range slices.Sorted(maps.Keys(override.Data)) {
		value, err := expandEnv(override.Data[key])
		if err != nil {
			return fmt.Errorf("%s data[%s]: %w", describe(original), key, err)
		}
		data[key] = base64.StdEncoding.EncodeToString([]byte(value))
		// The value itself is never recorded: it is a credential.
		r.rec.record(r.Name(), describe(original), "data."+key, "<previous>", "<overridden>")
	}
	for _, key := range override.RemoveData {
		if _, present := data[key]; present {
			delete(data, key)
			r.rec.record(r.Name(), describe(original), "data."+key, "<previous>", "<removed>")
		}
	}
	return unstructured.SetNestedMap(obj.Object, data, "data")
}

func (r *secretRule) applyToVolumeRefs(obj, original *unstructured.Unstructured) error {
	for _, field := range secretRefFields {
		namespace, _, err := unstructured.NestedString(original.Object, "spec", "csi", field, "namespace")
		if err != nil {
			continue
		}
		name, found, err := unstructured.NestedString(original.Object, "spec", "csi", field, "name")
		if err != nil || !found || name == "" {
			continue
		}
		_, newNamespace, newName, ok := r.lookup(namespace, name)
		if !ok {
			continue
		}
		if newName != name {
			if err := unstructured.SetNestedField(obj.Object, newName, "spec", "csi", field, "name"); err != nil {
				return err
			}
			r.rec.record(r.Name(), describe(original), "spec.csi."+field+".name", name, newName)
		}
		if newNamespace != namespace {
			if err := unstructured.SetNestedField(obj.Object, newNamespace, "spec", "csi", field, "namespace"); err != nil {
				return err
			}
			r.rec.record(r.Name(), describe(original), "spec.csi."+field+".namespace", namespace, newNamespace)
		}
	}
	return nil
}

func (r *secretRule) applyToClassParams(obj, original *unstructured.Unstructured) error {
	params, found, err := unstructured.NestedStringMap(original.Object, "parameters")
	if err != nil || !found {
		return nil
	}
	updated, _, err := unstructured.NestedStringMap(obj.Object, "parameters")
	if err != nil || updated == nil {
		return nil
	}

	changed := false
	for _, pair := range convert.SecretRefParamPairs {
		nameParam, namespaceParam := pair[0], pair[1]
		name, hasName := params[nameParam]
		namespace, hasNamespace := params[namespaceParam]
		if !hasName || !hasNamespace {
			continue
		}
		_, newNamespace, newName, ok := r.lookup(namespace, name)
		if !ok {
			// A templated reference such as ${pvc.namespace} cannot match a literal key.
			// It is left alone; the unused-mapping report will surface a rule that never
			// matched anything.
			continue
		}
		if newName != name {
			updated[nameParam] = newName
			r.rec.record(r.Name(), describe(original), "parameters."+nameParam, name, newName)
			changed = true
		}
		if newNamespace != namespace {
			updated[namespaceParam] = newNamespace
			r.rec.record(r.Name(), describe(original), "parameters."+namespaceParam, namespace, newNamespace)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return unstructured.SetNestedStringMap(obj.Object, updated, "parameters")
}

// --- mount options ------------------------------------------------------------------------

type mountOptionsRule struct {
	spec *MountOptionsSpec
	rec  *recorder
}

func (r *mountOptionsRule) Name() string { return "mountOptions" }

func (r *mountOptionsRule) Apply(obj, original *unstructured.Unstructured) error {
	if original.GetKind() != "PersistentVolume" {
		return nil
	}

	options, configured := r.spec.all, r.spec.allSet
	if r.spec.byPV != nil {
		perVolume, ok := r.spec.byPV[original.GetName()]
		if !ok {
			return nil
		}
		r.rec.use("mountOptions[" + original.GetName() + "]")
		options, configured = perVolume, true
	}
	if !configured {
		return nil
	}

	previous, _, _ := unstructured.NestedStringSlice(original.Object, "spec", "mountOptions")
	if len(options) == 0 {
		unstructured.RemoveNestedField(obj.Object, "spec", "mountOptions")
		if len(previous) > 0 {
			r.rec.record(r.Name(), describe(original), "spec.mountOptions", joinOptions(previous), "<cleared>")
		}
		return nil
	}

	if err := unstructured.SetNestedStringSlice(obj.Object, options, "spec", "mountOptions"); err != nil {
		return err
	}
	r.rec.record(r.Name(), describe(original), "spec.mountOptions", joinOptions(previous), joinOptions(options))
	return nil
}

func joinOptions(options []string) string {
	if len(options) == 0 {
		return "<none>"
	}
	out := options[0]
	for _, option := range options[1:] {
		out += "," + option
	}
	return out
}

// --- node affinity ------------------------------------------------------------------------

type nodeAffinityRule struct {
	spec *NodeAffinitySpec
	rec  *recorder
}

func (r *nodeAffinityRule) Name() string { return "nodeAffinity" }

func (r *nodeAffinityRule) Apply(obj, original *unstructured.Unstructured) error {
	if original.GetKind() != "PersistentVolume" {
		return nil
	}
	_, had, _ := unstructured.NestedMap(original.Object, "spec", "nodeAffinity")

	if r.spec.Remove {
		if had {
			unstructured.RemoveNestedField(obj.Object, "spec", "nodeAffinity")
			r.rec.record(r.Name(), describe(original), "spec.nodeAffinity", "<present>", "<removed>")
		}
		return nil
	}

	// Replaced wholesale rather than edited in place: the source term may name a topology
	// key the target cluster does not publish at all, so preserving its structure would
	// preserve the wrong constraint.
	expression := map[string]any{
		"key":      r.spec.Key,
		"operator": r.spec.effectiveOperator(),
	}
	if len(r.spec.Values) > 0 {
		values := make([]any, 0, len(r.spec.Values))
		for _, value := range r.spec.Values {
			values = append(values, value)
		}
		expression["values"] = values
	}
	affinity := map[string]any{
		"required": map[string]any{
			"nodeSelectorTerms": []any{
				map[string]any{"matchExpressions": []any{expression}},
			},
		},
	}
	if err := unstructured.SetNestedMap(obj.Object, affinity, "spec", "nodeAffinity"); err != nil {
		return err
	}

	from := "<none>"
	if had {
		from = "<previous>"
	}
	r.rec.record(r.Name(), describe(original), "spec.nodeAffinity", from, r.spec.Key+" "+r.spec.effectiveOperator())
	return nil
}

// --- metadata -------------------------------------------------------------------------------

type metadataRule struct {
	spec *MetadataSpec
	rec  *recorder
}

func (r *metadataRule) Name() string { return "metadata" }

func (r *metadataRule) applies(kind string) bool {
	if len(r.spec.Kinds) == 0 {
		return true
	}
	for _, candidate := range r.spec.Kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

func (r *metadataRule) Apply(obj, original *unstructured.Unstructured) error {
	if !r.applies(original.GetKind()) {
		return nil
	}
	if err := r.edit(obj, original, "annotations", r.spec.Annotations, obj.GetAnnotations, obj.SetAnnotations); err != nil {
		return err
	}
	return r.edit(obj, original, "labels", r.spec.Labels, obj.GetLabels, obj.SetLabels)
}

func (r *metadataRule) edit(
	obj, original *unstructured.Unstructured,
	field string,
	spec *MapEdit,
	get func() map[string]string,
	set func(map[string]string),
) error {
	if spec == nil {
		return nil
	}
	current := get()
	if current == nil {
		current = map[string]string{}
	}

	// Rename first, so that a set on the new key wins over the carried-over value.
	for _, from := range slices.Sorted(maps.Keys(spec.Rename)) {
		to := spec.Rename[from]
		value, present := current[from]
		if !present {
			continue
		}
		delete(current, from)
		current[to] = value
		r.rec.record(r.Name(), describe(original), "metadata."+field+"."+from, from, to)
	}
	for _, key := range slices.Sorted(maps.Keys(spec.Set)) {
		value, err := expandEnv(spec.Set[key])
		if err != nil {
			return fmt.Errorf("metadata.%s.set[%s]: %w", field, key, err)
		}
		previous, present := current[key]
		current[key] = value
		if !present {
			previous = "<none>"
		}
		r.rec.record(r.Name(), describe(original), "metadata."+field+"."+key, previous, value)
	}
	for _, key := range spec.Remove {
		if previous, present := current[key]; present {
			delete(current, key)
			r.rec.record(r.Name(), describe(original), "metadata."+field+"."+key, previous, "<removed>")
		}
	}

	if len(current) == 0 {
		unstructured.RemoveNestedField(obj.Object, "metadata", field)
		return nil
	}
	set(current)
	return nil
}
