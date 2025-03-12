package apiclient

import (
	"context"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestApiClient_ResolvePathToInode(t *testing.T) {
	apiClient := GetApiClientForTest(t)

	ctx := context.Background()
	fs, err := apiClient.GetFileSystemByName(ctx, "default")
	assert.NoError(t, err)
	assert.NotEmpty(t, fs)

	inode, err := apiClient.ResolvePathToInode(ctx, fs, "/")
	assert.NoError(t, err)
	assert.Positive(t, inode)

	// test non-existing path
	inode, err = apiClient.ResolvePathToInode(ctx, fs, "/test-not-existing")
	assert.Error(t, err)
	assert.IsType(t, ObjectNotFoundError, err)
	assert.Zero(t, inode)

	// test non-existing filesystem
	fs = &FileSystem{Uid: uuid.New()}
	inode, err = apiClient.ResolvePathToInode(ctx, fs, "/")
	assert.Error(t, err)
	assert.IsType(t, ObjectNotFoundError, err)
	assert.Zero(t, inode)

}
