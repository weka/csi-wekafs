// Package apply recreates exported objects on a target cluster.
//
// Objects are applied in dependency order and, by default, never overwrite anything that
// already exists: an import into a cluster that is not empty should report a collision
// rather than silently redefine a live volume.
package apply

import (
	"context"
	"fmt"
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
	"github.com/wekafs/csi-wekafs/pkg/migrator/transform"
)

// kindHandler knows how to check for and create one kind. Registering a kind here is the
// only change a new supported kind requires: the apply order, the existence check, the
// create call and the kinds reported by KnownKinds all derive from this table.
type kindHandler struct {
	// order places the kind in the apply sequence.
	//
	// The ordering is not cosmetic. Secrets and StorageClasses must exist before the
	// volumes that reference them, and a PersistentVolume must exist before its claim: a
	// claim applied first has no volume to bind to, so the control plane may dynamically
	// provision fresh empty storage against the StorageClass instead of adopting the
	// restored volume.
	order  int
	get    func(ctx context.Context, c kubernetes.Interface, namespace, name string) error
	create func(ctx context.Context, c kubernetes.Interface, obj *unstructured.Unstructured) error
}

var kindHandlers = map[string]kindHandler{
	"Secret": {
		order: 0,
		get: func(ctx context.Context, c kubernetes.Interface, namespace, name string) error {
			_, err := c.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
			return err
		},
		create: func(ctx context.Context, c kubernetes.Interface, obj *unstructured.Unstructured) error {
			return createTyped(obj, func(typed *corev1.Secret) error {
				_, err := c.CoreV1().Secrets(obj.GetNamespace()).Create(ctx, typed, metav1.CreateOptions{})
				return err
			})
		},
	},
	"StorageClass": {
		order: 1,
		get: func(ctx context.Context, c kubernetes.Interface, _, name string) error {
			_, err := c.StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{})
			return err
		},
		create: func(ctx context.Context, c kubernetes.Interface, obj *unstructured.Unstructured) error {
			return createTyped(obj, func(typed *storagev1.StorageClass) error {
				_, err := c.StorageV1().StorageClasses().Create(ctx, typed, metav1.CreateOptions{})
				return err
			})
		},
	},
	"PersistentVolume": {
		order: 2,
		get: func(ctx context.Context, c kubernetes.Interface, _, name string) error {
			_, err := c.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
			return err
		},
		create: func(ctx context.Context, c kubernetes.Interface, obj *unstructured.Unstructured) error {
			return createTyped(obj, func(typed *corev1.PersistentVolume) error {
				_, err := c.CoreV1().PersistentVolumes().Create(ctx, typed, metav1.CreateOptions{})
				return err
			})
		},
	},
	"PersistentVolumeClaim": {
		order: 3,
		get: func(ctx context.Context, c kubernetes.Interface, namespace, name string) error {
			_, err := c.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
			return err
		},
		create: func(ctx context.Context, c kubernetes.Interface, obj *unstructured.Unstructured) error {
			return createTyped(obj, func(typed *corev1.PersistentVolumeClaim) error {
				_, err := c.CoreV1().PersistentVolumeClaims(obj.GetNamespace()).Create(ctx, typed, metav1.CreateOptions{})
				return err
			})
		},
	},
}

// createTyped decodes an object into its typed form and hands it to create.
func createTyped[T any](obj *unstructured.Unstructured, create func(*T) error) error {
	raw, err := obj.MarshalJSON()
	if err != nil {
		return fmt.Errorf("re-encoding object: %w", err)
	}
	var typed T
	if err := yaml.Unmarshal(raw, &typed); err != nil {
		return err
	}
	return create(&typed)
}

// KnownKinds lists the kinds an import can apply, in apply order. Callers that summarise an
// archive use it so they cannot drift out of step with what is actually supported.
func KnownKinds() []string {
	kinds := make([]string, 0, len(kindHandlers))
	for kind := range kindHandlers {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool {
		return kindHandlers[kinds[i]].order < kindHandlers[kinds[j]].order
	})
	return kinds
}

// Options controls an import.
type Options struct {
	// DryRun reports what would happen without writing anything.
	DryRun bool
	// SkipExisting continues past objects that already exist instead of failing.
	SkipExisting bool
	// AllowRedactedSecrets permits importing an archive whose credentials were scrubbed.
	// Without it such an archive is refused, because the driver would fail to authenticate
	// at first mount with an error that points nowhere near the real cause.
	AllowRedactedSecrets bool
	// Transform rewrites each object on its way in. A nil chain, and the empty chain v1
	// supplies, both mean "recreate exactly as exported".
	Transform *transform.Chain
}

// Result is the per-object outcome of an import.
type Result struct {
	Kind      string
	Namespace string
	Name      string
	Action    string // "created", "exists", or "would create"
}

// Applier writes to a target cluster.
type Applier struct {
	client kubernetes.Interface
	opts   Options
}

// New returns an Applier writing through client.
func New(client kubernetes.Interface, opts Options) *Applier {
	return &Applier{client: client, opts: opts}
}

// Apply recreates every object in the archive.
func (a *Applier) Apply(ctx context.Context, r *archive.Reader) ([]Result, error) {
	objects, err := decode(r)
	if err != nil {
		return nil, err
	}

	// Transform before checking secrets, not after. A mapping file may supply the very
	// credentials that were redacted at export, which is the normal case for a move to a
	// different Weka cluster: the target has different credentials anyway.
	if err := a.transform(ctx, objects); err != nil {
		return nil, err
	}
	if err := a.checkSecrets(objects); err != nil {
		return nil, err
	}
	// Collisions are only knowable once every object has its final identity.
	if err := checkCollisions(objects); err != nil {
		return nil, err
	}

	var results []Result
	for _, kind := range KnownKinds() {
		for _, obj := range objectsOfKind(objects, kind) {
			result, err := a.applyOne(ctx, obj)
			if err != nil {
				// Report what already succeeded: a partial import is far easier to finish
				// than to diagnose from an error alone.
				return results, err
			}
			results = append(results, result)
		}
	}
	return results, nil
}

// transform rewrites every object, then reports what changed and what did not.
func (a *Applier) transform(ctx context.Context, objects []*unstructured.Unstructured) error {
	if a.opts.Transform == nil || a.opts.Transform.Len() == 0 {
		return nil
	}
	logger := zerolog.Ctx(ctx)
	logger.Info().Strs("rules", a.opts.Transform.Names()).Msg("Applying transform rules")

	for _, obj := range objects {
		if err := a.opts.Transform.Apply(obj); err != nil {
			return fmt.Errorf("transforming %s: %w", describe(obj), err)
		}
	}

	for _, change := range a.opts.Transform.Changes() {
		logger.Debug().Msg(change.String())
	}
	// A mapping that matched nothing is nearly always a typo. Left unreported, the operator
	// believes a rename happened and only finds out when a pod cannot mount.
	for _, unused := range a.opts.Transform.UnusedMappings() {
		logger.Warn().Str("mapping", unused).Msg("Transform mapping matched no object in the archive")
	}
	return nil
}

// checkCollisions refuses an import whose transformed objects would overwrite each other.
//
// The usual cause is collapsing several namespaces into one target namespace where two
// claims share a name. Without this the import would create one and then fail partway
// through on the second, leaving the cluster half-populated.
func checkCollisions(objects []*unstructured.Unstructured) error {
	seen := map[string]bool{}
	clashes := map[string]bool{}
	for _, obj := range objects {
		key := obj.GetKind() + " " + describe(obj)
		if seen[key] {
			clashes[key] = true
		}
		seen[key] = true
	}
	if len(clashes) == 0 {
		return nil
	}
	conflicts := make([]string, 0, len(clashes))
	for key := range clashes {
		conflicts = append(conflicts, key)
	}
	sort.Strings(conflicts)
	return fmt.Errorf("the transform would produce more than one object with the same identity: %s\n"+
		"this usually means several namespaces were collapsed into one where names are not unique; "+
		"rename the claims with persistentVolumeClaims, or map the namespaces separately",
		strings.Join(conflicts, "; "))
}

// checkSecrets refuses an archive whose credentials were redacted, naming the secrets so an
// operator can decide whether to create them by hand or re-export.
func (a *Applier) checkSecrets(objects []*unstructured.Unstructured) error {
	if a.opts.AllowRedactedSecrets {
		return nil
	}
	var offenders []string
	for _, obj := range objects {
		if obj.GetKind() != "Secret" {
			continue
		}
		if keys := convert.RedactedKeys(obj); len(keys) > 0 {
			offenders = append(offenders, fmt.Sprintf("%s/%s (%s)", obj.GetNamespace(), obj.GetName(), strings.Join(keys, ", ")))
		}
	}
	if len(offenders) == 0 {
		return nil
	}
	sort.Strings(offenders)
	return fmt.Errorf("archive was exported without --include-secret-data, so these secrets carry no usable credentials: %s\n"+
		"supply them with a transform file (secrets.<ns>/<name>.data), re-export with --include-secret-data, "+
		"or pass --allow-redacted-secrets and create the secrets yourself",
		strings.Join(offenders, "; "))
}

func (a *Applier) applyOne(ctx context.Context, obj *unstructured.Unstructured) (Result, error) {
	// Objects arrive already transformed, so this reports the identity they will actually be
	// created under.
	result := Result{Kind: obj.GetKind(), Namespace: obj.GetNamespace(), Name: obj.GetName()}

	exists, err := a.exists(ctx, obj)
	if err != nil {
		return result, err
	}
	if exists {
		if !a.opts.SkipExisting {
			return result, fmt.Errorf("%s %s already exists on the target cluster; "+
				"pass --skip-existing to leave it alone", obj.GetKind(), describe(obj))
		}
		result.Action = "exists"
		return result, nil
	}

	if a.opts.DryRun {
		result.Action = "would create"
		return result, nil
	}
	if err := a.create(ctx, obj); err != nil {
		return result, fmt.Errorf("creating %s %s: %w", obj.GetKind(), describe(obj), err)
	}
	zerolog.Ctx(ctx).Debug().
		Str("kind", obj.GetKind()).
		Str("object", describe(obj)).
		Msg("Created object")
	result.Action = "created"
	return result, nil
}

func (a *Applier) exists(ctx context.Context, obj *unstructured.Unstructured) (bool, error) {
	handler, ok := kindHandlers[obj.GetKind()]
	if !ok {
		return false, fmt.Errorf("unsupported kind %q in archive", obj.GetKind())
	}
	err := handler.get(ctx, a.client, obj.GetNamespace(), obj.GetName())
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking whether %s %s exists: %w", obj.GetKind(), describe(obj), err)
	}
	return true, nil
}

func (a *Applier) create(ctx context.Context, obj *unstructured.Unstructured) error {
	handler, ok := kindHandlers[obj.GetKind()]
	if !ok {
		return fmt.Errorf("unsupported kind %q", obj.GetKind())
	}
	return handler.create(ctx, a.client, obj)
}

// decode turns archive entries back into objects, in manifest order.
func decode(r *archive.Reader) ([]*unstructured.Unstructured, error) {
	var objects []*unstructured.Unstructured
	for _, entry := range r.Entries() {
		body, ok := r.Body(entry.Path)
		if !ok {
			return nil, fmt.Errorf("archive entry %q has no content", entry.Path)
		}
		var raw map[string]any
		if err := yaml.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("decoding %s: %w", entry.Path, err)
		}
		obj := &unstructured.Unstructured{Object: raw}
		if obj.GetKind() == "" {
			return nil, fmt.Errorf("%s declares no kind", entry.Path)
		}
		objects = append(objects, obj)
	}
	return objects, nil
}

func objectsOfKind(objects []*unstructured.Unstructured, kind string) []*unstructured.Unstructured {
	var out []*unstructured.Unstructured
	for _, obj := range objects {
		if obj.GetKind() == kind {
			out = append(out, obj)
		}
	}
	return out
}

func describe(obj *unstructured.Unstructured) string {
	if obj.GetNamespace() == "" {
		return obj.GetName()
	}
	return obj.GetNamespace() + "/" + obj.GetName()
}
