package wekafs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsEncryptedWithoutApiClient covers a volume the driver has no way to ask about: a
// filesystem-backed volume with no API client bound.
//
// Exists() reports (false, nil) for that case rather than an error, so isEncrypted walks straight
// past its error check, finds no API client to query, and leaves v.encrypted unset. Dereferencing
// it then panicked. The encryption state is genuinely unknown here, so the only safe answers are an
// error or a panic - and reporting "not encrypted" would be worse than either, since callers act on
// it.
func TestIsEncryptedWithoutApiClient(t *testing.T) {
	v := &Volume{FilesystemName: "some-filesystem"}
	require.True(t, v.isFilesystem(), "test needs a filesystem-backed volume to reach the path")
	require.Nil(t, v.apiClient, "test needs an unbound volume to reach the path")

	encrypted, err := v.isEncrypted(context.Background())

	assert.Error(t, err, "encryption state is unknown without an API client, and must not be reported as fact")
	assert.False(t, encrypted)
}

// TestIsEncryptedSafeWithoutApiClient pins the same case through the convenience wrapper. It
// discards the error by design, but it never discarded the panic.
func TestIsEncryptedSafeWithoutApiClient(t *testing.T) {
	v := &Volume{FilesystemName: "some-filesystem"}

	assert.False(t, v.isEncryptedSafe(context.Background()))
}

// TestIsEncryptedReturnsCachedValue confirms the guard leaves the already-resolved path alone: once
// the encryption state is known, it is returned without consulting anything.
func TestIsEncryptedReturnsCachedValue(t *testing.T) {
	for _, want := range []bool{true, false} {
		v := &Volume{FilesystemName: "some-filesystem", encrypted: &want}

		got, err := v.isEncrypted(context.Background())

		assert.NoError(t, err)
		assert.Equal(t, want, got)
	}
}
