package wekafs

import (
	"context"
	"encoding/json"
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

// quotaMissingHealth is what the probe reports for a volume that resolved but has no quota. Without
// it there is nothing to act on, so every gate test has to supply one.
func quotaMissingHealth() *VolumeHealth {
	return &VolumeHealth{Message: volumeNoQuotaMessage, QuotaMissing: true, InodeId: 4242}
}

// The reconciler writes to the Weka cluster only when explicitly told to. It is a background loop,
// so a default that mutates storage would surprise anyone upgrading.
func TestBackfillMissingQuotaIsOffByDefault(t *testing.T) {
	assert.False(t, NewDriverConfig(DriverConfigOptions{}).backfillMissingQuotas,
		"backfilling quotas must be opt-in")

	// With the setting off, a volume that genuinely has no quota is still left alone. The volume
	// carries no API client, so anything that got past the check would fail - a clean (false, nil)
	// is only reachable by returning at the gate.
	r := newBackfillReconciler(false)
	vol := &Volume{id: "weka/v2/testfs/no-quota"}

	created, err := r.backfillMissingQuota(context.Background(), vol, dynamicPV(), quotaMissingHealth())
	assert.False(t, created)
	assert.NoError(t, err)
}

// A probe that could not construct the volume hands back nil. The backfill must treat that as
// nothing to do rather than dereferencing it, since a failed probe is routine.
func TestBackfillMissingQuotaSkipsNilVolume(t *testing.T) {
	r := newBackfillReconciler(true)
	created, err := r.backfillMissingQuota(context.Background(), nil, nil, quotaMissingHealth())
	assert.False(t, created)
	assert.NoError(t, err)
}

// A volume that already has a quota must not cost a single extra API call. The probe has already
// established that, so the backfill returns on the probe result alone - passing a volume with no API
// client proves nothing further is attempted.
func TestBackfillDoesNothingWhenTheQuotaExists(t *testing.T) {
	r := newBackfillReconciler(true)
	vol := &Volume{id: "weka/v2/testfs/has-a-quota"}
	health := &VolumeHealth{Message: volumeHealthyMessage, Capacity: 1 << 30, InodeId: 4242}

	created, err := r.backfillMissingQuota(context.Background(), vol, dynamicPV(), health)
	assert.False(t, created)
	assert.NoError(t, err, "a volume with a quota must not be probed again")
}

// A probe that could not establish the condition reports no health at all. There is then nothing to
// act on, and guessing would be worse than waiting for the next sweep.
func TestBackfillDoesNothingWithoutAProbeResult(t *testing.T) {
	r := newBackfillReconciler(true)
	vol := &Volume{id: "weka/v2/testfs/unknown"}

	created, err := r.backfillMissingQuota(context.Background(), vol, dynamicPV(), nil)
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

	created, err := r.backfillMissingQuota(context.Background(), vol, staticPV(), quotaMissingHealth())
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

	created, err := r.backfillMissingQuota(context.Background(), vol, staticPV(), quotaMissingHealth())
	assert.False(t, created)
	assert.Error(t, err, "with both flags on the static volume must proceed past the gate")
}

// A dynamically provisioned volume never consults setQuotaOnStaticVolumes - backfillMissingQuotas
// alone is enough for it.
func TestDynamicVolumesDoNotNeedTheStaticFlag(t *testing.T) {
	r := newBackfillReconciler(true)
	vol := &Volume{id: "weka/v2/testfs/some-dynamic-dir"}

	created, err := r.backfillMissingQuota(context.Background(), vol, dynamicPV(), quotaMissingHealth())
	assert.False(t, created)
	assert.Error(t, err, "a dynamic volume must proceed past the gate on backfillMissingQuotas alone")
}

// The quota mode must come from the volume, not from a default. A volume provisioned with
// capacityEnforcement=SOFT that is quietly given a hard quota starts failing writes it was
// deliberately allowed to make.
func TestQuotaEnforcementComesFromTheVolumeContext(t *testing.T) {
	withAttrs := func(attrs map[string]string) *v1.PersistentVolume {
		return &v1.PersistentVolume{Spec: v1.PersistentVolumeSpec{
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CSI: &v1.CSIPersistentVolumeSource{Driver: "csi.weka.io", VolumeAttributes: attrs},
			},
		}}
	}

	testCases := []struct {
		name      string
		attrs     map[string]string
		expect    bool
		expectErr bool
	}{
		// This is what a real PersistentVolume carries: the StorageClass parameters are persisted
		// verbatim into volumeAttributes at provisioning time.
		{name: "HARD is enforced", attrs: map[string]string{"capacityEnforcement": "HARD"}, expect: true},
		{name: "SOFT is not enforced", attrs: map[string]string{"capacityEnforcement": "SOFT"}, expect: false},
		{name: "absent defaults to hard", attrs: map[string]string{"volumeType": "dir/v1"}, expect: true},
		{name: "empty attributes default to hard", attrs: map[string]string{}, expect: true},
		{
			// Better to leave the volume without a quota than to guess at the enforcement.
			name:      "an unusable value is an error, not a guess",
			attrs:     map[string]string{"capacityEnforcement": "SOMETHINGELSE"},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := quotaEnforcementFromPv(withAttrs(tc.attrs))
			if tc.expectErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expect, got)
		})
	}

	// A PersistentVolume with no CSI source at all must not panic.
	got, err := quotaEnforcementFromPv(&v1.PersistentVolume{})
	assert.NoError(t, err)
	assert.True(t, got)

	got, err = quotaEnforcementFromPv(nil)
	assert.NoError(t, err)
	assert.True(t, got)
}

// GetQuotaType is what "retain the existing enforcement" is derived from when a caller passes no
// preference, so its mapping has to be exact. Expanding a volume goes through that path, and a
// wrong answer there silently converts a soft quota to hard.
func TestQuotaTypeRoundTripsForRetain(t *testing.T) {
	// A hard quota has both limits equal - see NewQuotaCreateRequest.
	hard := &apiclient.Quota{HardLimitBytes: 1 << 30, SoftLimitBytes: 1 << 30}
	assert.Equal(t, apiclient.QuotaTypeHard, hard.GetQuotaType())
	assert.True(t, hard.GetQuotaType() == apiclient.QuotaTypeHard,
		"a hard quota must be retained as enforced")

	// A soft quota carries the maximum as its hard limit.
	soft := &apiclient.Quota{HardLimitBytes: apiclient.MaxQuotaSize, SoftLimitBytes: 1 << 30}
	assert.Equal(t, apiclient.QuotaTypeSoft, soft.GetQuotaType())
	assert.False(t, soft.GetQuotaType() == apiclient.QuotaTypeHard,
		"a soft quota must be retained as unenforced, not promoted to hard")
}

// A soft quota is only as useful as its grace period. It comes from the same persisted StorageClass
// parameters as the enforcement mode, and a volume built from an ID never had those applied - so
// without reading it here a volume asking for "block after 168h" would get "never block".
func TestQuotaGracePeriodComesFromTheVolumeContext(t *testing.T) {
	withAttrs := func(attrs map[string]string) *v1.PersistentVolume {
		return &v1.PersistentVolume{Spec: v1.PersistentVolumeSpec{
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CSI: &v1.CSIPersistentVolumeSource{Driver: "csi.weka.io", VolumeAttributes: attrs},
			},
		}}
	}

	testCases := []struct {
		name      string
		attrs     map[string]string
		expect    uint64
		expectErr bool
	}{
		{
			name:   "a week, as a Go duration",
			attrs:  map[string]string{"capacityEnforcement": "SOFT", "quotaGracePeriod": "168h"},
			expect: 168 * 60 * 60,
		},
		{name: "minutes", attrs: map[string]string{"quotaGracePeriod": "30m"}, expect: 1800},
		{name: "compound", attrs: map[string]string{"quotaGracePeriod": "1h30m"}, expect: 5400},
		// Absent means advisory-only, which is what provisioning does with an unset parameter.
		{name: "absent is zero", attrs: map[string]string{"capacityEnforcement": "SOFT"}, expect: 0},
		{
			name:      "an unparseable duration is an error, not a guess",
			attrs:     map[string]string{"quotaGracePeriod": "one week"},
			expectErr: true,
		},
		{
			name:      "a negative duration is rejected",
			attrs:     map[string]string{"quotaGracePeriod": "-1h"},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := quotaGracePeriodFromPv(withAttrs(tc.attrs))
			if tc.expectErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expect, got)
		})
	}

	got, err := quotaGracePeriodFromPv(&v1.PersistentVolume{})
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), got)
}

// The grace period is carried on the Volume, because that is where setQuota reads it. Proving the
// backfill sets it before creating the quota is what stops a soft quota being created with 0.
func TestBackfillCarriesTheGracePeriodOntoTheVolume(t *testing.T) {
	r := newBackfillReconciler(true)
	vol := &Volume{id: "weka/v2/testfs/soft-vol"}
	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{provisionedByAnnotation: "csi.weka.io"}},
		Spec: v1.PersistentVolumeSpec{
			Capacity: v1.ResourceList{v1.ResourceStorage: resource.MustParse("1Gi")},
			PersistentVolumeSource: v1.PersistentVolumeSource{CSI: &v1.CSIPersistentVolumeSource{
				Driver: "csi.weka.io",
				VolumeAttributes: map[string]string{
					"capacityEnforcement": "SOFT",
					"quotaGracePeriod":    "168h",
				},
			}},
		},
	}

	// The volume has no API client, so quota creation itself fails - but the grace period is set on
	// the volume before that point, which is what this asserts.
	_, err := r.backfillMissingQuota(context.Background(), vol, pv, quotaMissingHealth())
	assert.Error(t, err, "expected the unbound API client to stop it at the creation step")
	assert.Equal(t, uint64(168*60*60), vol.quotaGracePeriodSeconds,
		"the grace period must reach the volume before the quota is created")
}

// The cluster reports grace_seconds as null for a hard quota. That must unmarshal as 0 rather than
// failing the whole quota read, which would make every hard-quota volume look unreadable.
func TestQuotaParsesNullGraceSeconds(t *testing.T) {
	var q apiclient.Quota
	err := json.Unmarshal([]byte(`{"inode_id":42,"hard_limit_bytes":1073741824,"soft_limit_bytes":1073741824,"grace_seconds":null,"status":"ACTIVE"}`), &q)
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), q.GraceSeconds)
	assert.Equal(t, apiclient.QuotaTypeHard, q.GetQuotaType())

	err = json.Unmarshal([]byte(`{"inode_id":42,"hard_limit_bytes":9223372036854775807,"soft_limit_bytes":1073741824,"grace_seconds":604800,"status":"ACTIVE"}`), &q)
	assert.NoError(t, err)
	assert.Equal(t, uint64(604800), q.GraceSeconds)
	assert.Equal(t, apiclient.QuotaTypeSoft, q.GetQuotaType())
}

// The precedence rule for an expand: an explicit capacityEnforcement in the volume attributes wins
// over whatever the quota currently is; its absence means retain, so the default of hard cannot
// silently convert an existing soft quota.
func TestQuotaEnforcementPrecedenceOnExpand(t *testing.T) {
	// nil from getCapacityEnforcementParam's perspective is the "absent" case, which the caller
	// turns into a nil *bool. These assertions pin the distinction the rule depends on: absent and
	// explicit HARD both parse to true, so presence has to be tested separately from the value.
	absent := map[string]string{"volumeType": "dir/v1"}
	explicitHard := map[string]string{capacityEnforcementParam: "HARD"}
	explicitSoft := map[string]string{capacityEnforcementParam: "SOFT"}

	_, absentPresent := absent[capacityEnforcementParam]
	_, hardPresent := explicitHard[capacityEnforcementParam]
	assert.False(t, absentPresent, "absent must be distinguishable from explicit")
	assert.True(t, hardPresent)

	v, err := getCapacityEnforcementParam(absent)
	assert.NoError(t, err)
	assert.True(t, v, "absent parses to hard, which is why presence must be checked separately")

	v, err = getCapacityEnforcementParam(explicitHard)
	assert.NoError(t, err)
	assert.True(t, v)

	v, err = getCapacityEnforcementParam(explicitSoft)
	assert.NoError(t, err)
	assert.False(t, v)
}

// When capacityEnforcement is explicit, the whole quota spec comes from the volume attributes -
// including an absent quotaGracePeriod, which is the documented default of "advisory only" rather
// than a gap to be filled from the existing quota.
func TestExplicitEnforcementTakesGraceFromTheSameSource(t *testing.T) {
	attrs := map[string]string{capacityEnforcementParam: "SOFT"}
	grace, err := getQuotaGracePeriodParam(attrs)
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), grace,
		"an absent grace alongside an explicit enforcement is a real value, not a gap")

	attrs["quotaGracePeriod"] = "168h"
	grace, err = getQuotaGracePeriodParam(attrs)
	assert.NoError(t, err)
	assert.Equal(t, uint64(168*60*60), grace)
}
