package apiclient

import (
	"context"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestGetKmsWhenNotDefined(t *testing.T) {
	apiClient := GetApiClientForTest(t)
	ctx := context.Background()
	kms, err := apiClient.GetKmsConfiguration(ctx)
	assert.Error(t, err, "Expected error when getting KMS since it is not defined")
	assert.Equal(t, "KMS configuration is not supported", err.Error(), "Expected error message to indicate KMS is not supported")
	assert.Equal(t, (*Kms)(nil), kms, "Expected nil KMS")
}

func TestHasKmsConfigurationWhenNotPresent(t *testing.T) {
	apiClient := GetApiClientForTest(t)
	ctx := context.Background()
	hasKms := apiClient.HasKmsConfiguration(ctx)
	assert.False(t, hasKms, "Expected KMS configuration to not be present")
}

func TestIsEncryptionEnabledWhenNotPresent(t *testing.T) {
	apiClient := GetApiClientForTest(t)
	isEnabled := apiClient.IsEncryptionEnabled()
	assert.False(t, isEnabled, "Expected encryption to be disabled")
}
