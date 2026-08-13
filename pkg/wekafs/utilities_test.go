package wekafs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetQuotaGracePeriodParam(t *testing.T) {
	// param absent → (0, nil)
	secs, err := getQuotaGracePeriodParam(map[string]string{})
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), secs)

	// "72h" → (259200, nil)
	secs, err = getQuotaGracePeriodParam(map[string]string{"quotaGracePeriod": "72h"})
	assert.NoError(t, err)
	assert.Equal(t, uint64(259200), secs)

	// "30m" → (1800, nil)
	secs, err = getQuotaGracePeriodParam(map[string]string{"quotaGracePeriod": "30m"})
	assert.NoError(t, err)
	assert.Equal(t, uint64(1800), secs)

	// "0s" → (0, nil)
	secs, err = getQuotaGracePeriodParam(map[string]string{"quotaGracePeriod": "0s"})
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), secs)

	// "-5m" → error
	secs, err = getQuotaGracePeriodParam(map[string]string{"quotaGracePeriod": "-5m"})
	assert.Error(t, err)
	assert.Equal(t, uint64(0), secs)

	// "garbage" → error
	secs, err = getQuotaGracePeriodParam(map[string]string{"quotaGracePeriod": "garbage"})
	assert.Error(t, err)
	assert.Equal(t, uint64(0), secs)
}
