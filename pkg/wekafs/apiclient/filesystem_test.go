package apiclient

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestFileSystemResizeRequest_ThickMarshalsTotalCapacityOnly(t *testing.T) {
	cap := int64(300 * 1024 * 1024 * 1024)
	r := NewFileSystemResizeRequest(uuid.New(), &cap)
	b, err := json.Marshal(r)
	assert.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, "total_capacity")
	assert.NotContains(t, s, "thin_provision_min_ssd")
	assert.NotContains(t, s, "thin_provision_max_ssd")
}

func TestFileSystemResizeRequest_ThinMarshalsThinParams(t *testing.T) {
	cap := int64(300 * 1024 * 1024 * 1024)
	min := int64(1 * 1024 * 1024 * 1024)
	max := int64(9 * 1024 * 1024 * 1024)
	r := NewFileSystemResizeRequest(uuid.New(), &cap)
	r.ThinProvisionMinSsd = &min
	r.ThinProvisionMaxSsd = &max
	b, err := json.Marshal(r)
	assert.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, "total_capacity")
	assert.Contains(t, s, "thin_provision_min_ssd")
	assert.Contains(t, s, "thin_provision_max_ssd")
}
