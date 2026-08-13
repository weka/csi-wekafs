package wekafs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestConditionsAreReportedIndependentlyOfAbnormal is the point of the conditions series: the
// reportAs...Abnormal settings decide whether a namespace user gets a Kubernetes event, not whether
// the driver may record what it found. Before this, a fleet with the flags off reported every
// quota-less volume as healthy and offered no way to count them.
func TestConditionsAreReportedIndependentlyOfAbnormal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		health *VolumeHealth
		want   []string
	}{
		{
			name:   "no quota, not reported as abnormal",
			health: &VolumeHealth{QuotaMissing: true, Abnormal: false},
			want:   []string{volumeConditionNoQuota},
		},
		{
			name:   "no quota, reported as abnormal",
			health: &VolumeHealth{QuotaMissing: true, Abnormal: true},
			want:   []string{volumeConditionNoQuota},
		},
		{
			name:   "quota mismatch, not reported as abnormal",
			health: &VolumeHealth{QuotaMismatch: true, Abnormal: false},
			want:   []string{volumeConditionQuotaMismatch},
		},
		{
			name:   "no API client, not reported as abnormal",
			health: &VolumeHealth{NoApiClient: true, Abnormal: false},
			want:   []string{volumeConditionNoApiClient},
		},
		{
			// A missing filesystem or directory has no flag - it is always abnormal - so it has no
			// condition of its own and is reported under a generic one rather than silently omitted.
			name:   "abnormal for a reason with no flag",
			health: &VolumeHealth{Abnormal: true, Message: volumeFilesystemMissingMessage},
			want:   []string{volumeConditionUnavailable},
		},
		{
			name:   "several at once",
			health: &VolumeHealth{NoApiClient: true, QuotaMissing: true, QuotaMismatch: true},
			want:   []string{volumeConditionNoApiClient, volumeConditionNoQuota, volumeConditionQuotaMismatch},
		},
		{name: "healthy volume has none", health: &VolumeHealth{Message: volumeHealthyMessage}, want: nil},
		{name: "no probe result at all", health: nil, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.health.Conditions())
		})
	}
}

// TestVolumeConditionLabelValuesDoesNotAliasTheCache pins a bug that would be invisible in a single
// condition and silent with two: appending the condition onto the cache entry's own slice would
// write into its spare capacity, so the second condition would overwrite the first one's series key
// and one of the two volumes' conditions would vanish.
func TestVolumeConditionLabelValuesDoesNotAliasTheCache(t *testing.T) {
	base := make([]string, len(LabelsForCsiVolumes), len(LabelsForCsiVolumes)+4) // deliberate spare capacity
	for i := range LabelsForCsiVolumes {
		base[i] = "v"
	}

	first := volumeConditionLabelValues(base, volumeConditionNoQuota)
	second := volumeConditionLabelValues(base, volumeConditionQuotaMismatch)

	assert.Equal(t, volumeConditionNoQuota, first[len(first)-1],
		"building a second condition must not have overwritten the first")
	assert.Equal(t, volumeConditionQuotaMismatch, second[len(second)-1])
	assert.Len(t, base, len(LabelsForCsiVolumes), "the caller's slice must be left alone")
	assert.Nil(t, volumeConditionLabelValues(nil, volumeConditionNoQuota),
		"a volume with no labels has no series to key on")
}

// TestConditionCacheClearsResolvedConditions covers the other half: a condition that goes away must
// have its series deleted, or a volume that recovered keeps reporting the problem forever.
func TestConditionCacheClearsResolvedConditions(t *testing.T) {
	cache := newVolumeConditionCache()
	labels := make([]string, len(LabelsForCsiVolumes))

	cleared := cache.store("h", volumeConditionEntry{labels: labels, conditions: []string{volumeConditionNoQuota, volumeConditionQuotaMismatch}})
	assert.Empty(t, cleared, "nothing was known before, so nothing cleared")

	// The quota was created; the mismatch remains.
	cleared = cache.store("h", volumeConditionEntry{labels: labels, conditions: []string{volumeConditionQuotaMismatch}})
	assert.Equal(t, []string{volumeConditionNoQuota}, cleared)

	// Fully recovered.
	cleared = cache.store("h", volumeConditionEntry{labels: labels})
	assert.Equal(t, []string{volumeConditionQuotaMismatch}, cleared)

	// A volume that disappears reports its conditions so the caller can delete those series too.
	_, removed, _ := cache.retainOnly(map[string]struct{}{})
	assert.Len(t, removed, 1)
	assert.Equal(t, labels, removed[0].labels)
}
