package wekafs

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

func newBackfillReconciler(enabled bool) *volumeHealthReconciler {
	cs := &ControllerServer{config: NewDriverConfig(DriverConfigOptions{BackfillMissingQuotas: enabled})}
	return &volumeHealthReconciler{cs: cs, cache: newVolumeConditionCache()}
}

func dynamicPV() *v1.PersistentVolume {
	return &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{
		Name:        "pvc-dynamic",
		Annotations: map[string]string{provisionedByAnnotation: "csi.weka.io"},
	}}
}

func staticPV() *v1.PersistentVolume {
	// No provisioned-by annotation: an administrator wrote this one.
	return &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv-static"}}
}

// The reconciler writes to the Weka cluster only when explicitly told to. It is a background loop,
// so a default that mutates storage would surprise anyone upgrading.
func TestBackfillMissingQuotaIsOffByDefault(t *testing.T) {
	assert.False(t, NewDriverConfig(DriverConfigOptions{}).backfillMissingQuotas,
		"backfilling quotas must be opt-in")

	r := newBackfillReconciler(false)
	// A nil volume would panic if the disabled check did not come first.
	created, err := r.backfillMissingQuota(context.Background(), nil, nil)
	assert.False(t, created)
	assert.NoError(t, err)
}

// A probe that could not construct the volume hands back nil. The backfill must treat that as
// nothing to do rather than dereferencing it, since a failed probe is routine.
func TestBackfillMissingQuotaSkipsNilVolume(t *testing.T) {
	r := newBackfillReconciler(true)
	created, err := r.backfillMissingQuota(context.Background(), nil, nil)
	assert.False(t, created)
	assert.NoError(t, err)
}

// The quota must be sized from the PersistentVolume, never from the probed capacity: a volume with
// no quota has no limit for the backend to report, so the probe figure is exactly the wrong number.
func TestPvCapacityBytesIsTheBackfillSource(t *testing.T) {
	pv := &v1.PersistentVolume{}
	assert.Equal(t, int64(0), pvCapacityBytes(pv),
		"a PersistentVolume with no capacity gives nothing to size a quota from")

	pv.Spec.Capacity = v1.ResourceList{v1.ResourceStorage: resource.MustParse("3Gi")}
	assert.Equal(t, int64(3)<<30, pvCapacityBytes(pv))
}

// Each reason a backfill cannot happen has a different fix. Reporting only "unsupported" would send
// an operator looking in the wrong place, so every branch must name what to actually do.
func TestQuotaBackfillRemedyNamesTheFix(t *testing.T) {
	testCases := []struct {
		name        string
		support     apiclient.QuotaOnNonEmptyDirectorySupport
		mustContain []string
	}{
		{
			name:        "no data services container",
			support:     apiclient.QuotaOnNonEmptyDirectoryNoContainer,
			mustContain: []string{"data services container", "deploy"},
		},
		{
			name:    "cluster too old",
			support: apiclient.QuotaOnNonEmptyDirectoryVersionTooOld,
			mustContain: []string{
				apiclient.MinimumSupportedWekaVersions.DataServicesContainer,
				"upgrade",
				// The escape hatch has to be a command that can be run as written.
				"weka fs quota set",
				"--filesystem testfs",
				"--type directory",
				"--hard 3221225472",
			},
		},
		{
			name:        "undetermined",
			support:     apiclient.QuotaOnNonEmptyDirectoryUnknown,
			mustContain: []string{"could not determine"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := quotaBackfillRemedy(tc.support, "testfs", 3<<30)
			for _, want := range tc.mustContain {
				assert.True(t, strings.Contains(got, want),
					"remedy must mention %q, got: %s", want, got)
			}
		})
	}
}

// Kubernetes stamps provisioned-by onto anything a CSI provisioner created, and never onto a
// PersistentVolume written by hand. That annotation is the whole basis for treating the two
// differently, so it is worth pinning.
func TestIsStaticallyProvisioned(t *testing.T) {
	assert.False(t, isStaticallyProvisioned(dynamicPV()),
		"a PersistentVolume carrying provisioned-by was created by the provisioner")
	assert.True(t, isStaticallyProvisioned(staticPV()),
		"a PersistentVolume with no provisioned-by annotation is administrator-written")

	// An empty annotation map is the same as no annotation at all.
	pv := staticPV()
	pv.Annotations = map[string]string{}
	assert.True(t, isStaticallyProvisioned(pv))

	// Some other annotation must not be mistaken for provisioned-by.
	pv.Annotations = map[string]string{"pv.kubernetes.io/bound-by-controller": "yes"}
	assert.True(t, isStaticallyProvisioned(pv))

	assert.False(t, isStaticallyProvisioned(nil), "a nil PersistentVolume is not a static volume")
}

// A static volume is administrator-managed: the driver never created it and never gave it a quota,
// and giving it one starts enforcing a limit that was not being enforced before. So it needs its own
// consent, and backfillMissingQuotas alone must not be enough.
func TestStaticVolumesNeedTheirOwnFlag(t *testing.T) {
	cfg := NewDriverConfig(DriverConfigOptions{})
	assert.False(t, cfg.setQuotaOnStaticVolumes, "setting quotas on static volumes must be opt-in")

	// Backfill on, static off: a static volume must be left alone.
	//
	// The volume is deliberately non-nil but carries no API client. Anything that gets past the
	// static check reaches getQuota, which fails on the unbound client - so a clean (false, nil) is
	// only reachable by returning at the gate. Compare with the both-flags-on case below, which does
	// reach it and reports that error.
	r := newBackfillReconciler(true)
	vol := &Volume{id: "weka/v2/testfs/some-static-dir"}

	created, err := r.backfillMissingQuota(context.Background(), vol, staticPV())
	assert.False(t, created, "a static volume must not be given a quota without its own flag")
	assert.NoError(t, err, "the static check must return before anything touches the API client")
}

// With both flags on, a static volume is no longer skipped: it proceeds into the quota lookup, which
// is what the second flag is for. The unbound API client is what makes that observable - reaching it
// at all proves the gate was passed rather than silently short-circuiting as above.
func TestStaticVolumesProceedWhenBothFlagsAreOn(t *testing.T) {
	cs := &ControllerServer{config: NewDriverConfig(DriverConfigOptions{
		BackfillMissingQuotas:   true,
		SetQuotaOnStaticVolumes: true,
	})}
	r := &volumeHealthReconciler{cs: cs, cache: newVolumeConditionCache()}
	vol := &Volume{id: "weka/v2/testfs/some-static-dir"}

	created, err := r.backfillMissingQuota(context.Background(), vol, staticPV())
	assert.False(t, created)
	assert.Error(t, err, "with both flags on the static volume must proceed past the gate")
}

// A dynamically provisioned volume never consults setQuotaOnStaticVolumes - backfillMissingQuotas
// alone is enough for it.
func TestDynamicVolumesDoNotNeedTheStaticFlag(t *testing.T) {
	r := newBackfillReconciler(true)
	vol := &Volume{id: "weka/v2/testfs/some-dynamic-dir"}

	created, err := r.backfillMissingQuota(context.Background(), vol, dynamicPV())
	assert.False(t, created)
	assert.Error(t, err, "a dynamic volume must proceed past the gate on backfillMissingQuotas alone")
}
