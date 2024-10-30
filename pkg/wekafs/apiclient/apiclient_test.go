package apiclient

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestGenerateHash(t *testing.T) {
	credentials := Credentials{
		Username:  "testuser",
		Password:  "testpassword",
		Endpoints: []string{"127.0.0.1:14000"},
	}
	apiClient := &ApiClient{
		Credentials: credentials,
	}

	hash := apiClient.generateHash()
	assert.NotZero(t, hash, "Expected non-zero hash value")

	// Test that the hash is consistent for the same credentials
	hash2 := apiClient.generateHash()
	assert.Equal(t, hash, hash2, "Expected hash values to be equal for the same credentials")

	// Test that the hash changes for different credentials
	apiClient.Credentials.Username = "differentuser"
	hash3 := apiClient.generateHash()
	assert.NotEqual(t, hash, hash3, "Expected hash values to be different for different credentials")
}
