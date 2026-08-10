package wekafs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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

// A quota creation that failed is explained by what the cluster can do, and each case has a
// different fix. Reporting only "failed" would send an operator looking in the wrong place, so every
// branch must name what to actually do.
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
			// The cluster can colour an existing directory, so a failure here is something else -
			// say so, rather than sending the operator to look at data services.
			name:        "cluster is capable, so this is not a data services problem",
			support:     apiclient.QuotaOnNonEmptyDirectorySupported,
			mustContain: []string{"not a data services problem"},
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

// A volume with no API credentials reports unknown by default, and abnormal only when asked.
//
// The distinction matters: unknown means the driver could not look, abnormal asserts that it looked
// and found a problem. Defaulting to abnormal would blame the volume for a credentials gap.
func TestNoApiClientConditionIsOptInAbnormal(t *testing.T) {
	assert.False(t, NewDriverConfig(DriverConfigOptions{}).reportNoApiClientAsAbnormal,
		"reporting volumes without API credentials as abnormal must be opt-in")

	// The message has to name the fix, since the operator's action is on the StorageClass rather
	// than on the volume.
	assert.Contains(t, volumeNoApiClientMessage, "API secret")
	assert.Contains(t, volumeNoApiClientMessage, "StorageClass")
	// And it must not read as though the volume itself is broken.
	assert.Contains(t, volumeNoApiClientMessage, "cannot determine its condition")

	// The two abnormal-reporting settings are independent - one is about a volume the driver cannot
	// see, the other about a volume it can see but is not enforcing.
	cfg := NewDriverConfig(DriverConfigOptions{ReportNoApiClientAsAbnormal: true})
	assert.True(t, cfg.reportNoApiClientAsAbnormal)
	assert.False(t, cfg.reportNoQuotaAsAbnormal)

	cfg = NewDriverConfig(DriverConfigOptions{ReportNoQuotaAsAbnormal: true})
	assert.True(t, cfg.reportNoQuotaAsAbnormal)
	assert.False(t, cfg.reportNoApiClientAsAbnormal)
}

// describeVolume is what turns "no credentials" into a condition, so drive it both ways. A nil
// manager makes the PV lookup irrelevant here; what matters is which of the two branches is taken.
func TestDescribeVolumeReportsNoApiClientPerSetting(t *testing.T) {
	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-no-creds"},
		Spec: v1.PersistentVolumeSpec{
			Capacity: v1.ResourceList{v1.ResourceStorage: resource.MustParse("1Gi")},
			PersistentVolumeSource: v1.PersistentVolumeSource{CSI: &v1.CSIPersistentVolumeSource{
				Driver:       "csi.weka.io",
				VolumeHandle: "weka/v2/testfs/no-creds",
			}},
		},
	}

	for _, tc := range []struct {
		name           string
		report         bool
		expectAbnormal bool
		expectNil      bool
	}{
		{name: "default reports unknown", report: false, expectNil: true},
		{name: "opted in reports abnormal", report: true, expectAbnormal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := &ControllerServer{
				config:      NewDriverConfig(DriverConfigOptions{ReportNoApiClientAsAbnormal: tc.report}),
				api:         NewApiStore(NewDriverConfig(DriverConfigOptions{}), "test", "csi.weka.io"),
				secretCache: newSecretCache(time.Minute),
			}

			volume, condition, vol, health, _, err := cs.describeVolume(context.Background(), pv, nil)
			assert.NoError(t, err)
			assert.Nil(t, vol, "no volume can be built without an API client")
			assert.Nil(t, health)
			assert.Equal(t, int64(1)<<30, volume.CapacityBytes,
				"the declared capacity is still reported when the condition is not known")

			if tc.expectNil {
				assert.Nil(t, condition, "unknown is a nil condition")
				return
			}
			assert.NotNil(t, condition)
			assert.True(t, condition.Abnormal)
			assert.Equal(t, volumeNoApiClientMessage, condition.Message)
		})
	}
}

// The mismatch message has to name the fix and both numbers, since which way the discrepancy runs
// changes what the operator should think about it.
func TestQuotaMismatchMessage(t *testing.T) {
	msg := quotaMismatchMessage(10<<30, 1<<30)
	assert.Contains(t, msg, "10737418240")
	assert.Contains(t, msg, "1073741824")
	assert.Contains(t, msg, "expand the PersistentVolumeClaim")
}

// Reporting a mismatch is opt-in, and reporting is all it ever does - the quota is not touched.
func TestQuotaMismatchIsReportOnlyAndOptIn(t *testing.T) {
	assert.False(t, NewDriverConfig(DriverConfigOptions{}).reportQuotaMismatchAsAbnormal,
		"reporting quota mismatches must be opt-in")

	// There is deliberately no setting that resets a quota to the PersistentVolume's capacity.
	// Shrinking a hard quota under a volume that is already using more than its declared capacity
	// blocks its writes, and that is the case an administrator most often creates on purpose.
	cfg := NewDriverConfig(DriverConfigOptions{ReportQuotaMismatchAsAbnormal: true})
	assert.True(t, cfg.reportQuotaMismatchAsAbnormal)
	assert.False(t, cfg.backfillMissingQuotas,
		"reporting a mismatch must not imply any write to the cluster")
}

// A soft quota is stored with its hard limit at the maximum, so comparing the wrong field reports
// every soft-quota volume in the fleet as an exabyte-scale mismatch. GetCapacityLimit is what makes
// the comparison meaningful, and this pins it.
func TestSoftQuotaIsNotAMismatch(t *testing.T) {
	declared := int64(1) << 30

	soft := &apiclient.Quota{SoftLimitBytes: uint64(declared), HardLimitBytes: apiclient.MaxQuotaSize}
	assert.Equal(t, apiclient.QuotaTypeSoft, soft.GetQuotaType())
	assert.Equal(t, uint64(declared), soft.GetCapacityLimit(),
		"a soft quota's capacity is its soft limit, not the maximum stored as its hard limit")
	assert.NotEqual(t, soft.HardLimitBytes, uint64(declared),
		"comparing the hard limit instead would report a mismatch of nearly 8 exabytes")

	hard := &apiclient.Quota{SoftLimitBytes: uint64(declared), HardLimitBytes: uint64(declared)}
	assert.Equal(t, uint64(declared), hard.GetCapacityLimit())
}

// stripUnnecessaryPVFields runs on every PersistentVolume the controller caches, and everything it
// drops becomes invisible to every consumer of that cache - as an absent value, not an error.
//
// This is not hypothetical. It dropped ObjectMeta.Annotations and Spec.CSI.VolumeAttributes, which
// made isStaticallyProvisioned answer "static" for every volume in the fleet and quietly disabled
// the backfill, while quotaEnforcementFromPv returned the default for every one of them. Nothing
// failed; the work simply never happened. Only a live cluster showed it.
func TestStripUnnecessaryPVFieldsKeepsWhatTheDriverReads(t *testing.T) {
	full := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "pvc-1234",
			Annotations: map[string]string{provisionedByAnnotation: "csi.weka.io"},
		},
		Spec: v1.PersistentVolumeSpec{
			Capacity: v1.ResourceList{v1.ResourceStorage: resource.MustParse("1Gi")},
			PersistentVolumeSource: v1.PersistentVolumeSource{CSI: &v1.CSIPersistentVolumeSource{
				Driver:       "csi.weka.io",
				VolumeHandle: "dir/v1/testfs/csi-volumes/pvc-1234",
				VolumeAttributes: map[string]string{
					capacityEnforcementParam: "SOFT",
					"quotaGracePeriod":       "168h",
				},
			}},
		},
	}

	out, err := stripUnnecessaryPVFields(full)
	assert.NoError(t, err)
	stripped, ok := out.(*v1.PersistentVolume)
	assert.True(t, ok)

	// The dynamic/static distinction, which decides whether a volume is repaired at all.
	assert.False(t, isStaticallyProvisioned(stripped),
		"the provisioned-by annotation must survive caching, or every volume looks static")

	// The quota's shape, which decides what kind of quota a repaired volume gets.
	enforce, err := quotaEnforcementFromPv(stripped)
	assert.NoError(t, err)
	assert.False(t, enforce, "capacityEnforcement must survive caching")

	grace, err := quotaGracePeriodFromPv(stripped)
	assert.NoError(t, err)
	assert.Equal(t, uint64(168*60*60), grace, "quotaGracePeriod must survive caching")

	// And the capacity a repaired quota is sized from.
	assert.Equal(t, int64(1)<<30, pvCapacityBytes(stripped))
	assert.Equal(t, "dir/v1/testfs/csi-volumes/pvc-1234", stripped.Spec.CSI.VolumeHandle)
}
