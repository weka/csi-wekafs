package db

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *SqliteDatabase {
	_ = os.Remove(DBPath)
	db, err := gorm.Open(sqlite.Open("file:"+DBPath), &gorm.Config{})
	assert.NoError(t, err)

	err = db.AutoMigrate(&PvcAttachment{})
	assert.NoError(t, err)

	return &SqliteDatabase{db}
}

func TestDatabaseWrapper_GetAttachmentsByVolumeIdOrTargetPath(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	volumeId := uuid.New().String()
	targetPath := "/test/path"
	attachment := &PvcAttachment{
		VolumeId:   volumeId,
		TargetPath: targetPath,
		Node:       "node1",
		BootID:     "boot1",
		AccessType: "ReadWriteOnce",
	}

	// same attachment exactly
	attachment2 := &PvcAttachment{
		VolumeId:   volumeId,
		TargetPath: targetPath,
		Node:       "node1",
		BootID:     "boot1",
		AccessType: "ReadWriteOnce",
	}

	// attachment with different accesstype
	attachment3 := &(*attachment2)
	attachment3.AccessType = "ReadOnlyMany"

	// attachment with different ID but same other values, should fail on unique constraints of volumeId and targetPath
	attachment4 := &(*attachment3)
	attachment4.ID = 10

	err := db.CreateAttachment(ctx, attachment)
	assert.NoError(t, err)

	err = db.UpdateAttachment(ctx, attachment2)
	assert.NoError(t, err)

	err = db.UpdateAttachment(ctx, attachment3)
	assert.NoError(t, err)

	err = db.CreateOrUpdateAttachment(ctx, attachment3)
	assert.NoError(t, err)

	err = db.CreateAttachment(ctx, attachment4)
	assert.Error(t, err)

	// Test by volumeId
	attachments, err := db.GetAttachmentsByVolumeIdOrTargetPath(ctx, &volumeId, nil)
	assert.NoError(t, err)
	assert.Len(t, *attachments, 1)
	assert.Equal(t, *attachment3, (*attachments)[0])

	// Test by targetPath
	attachments, err = db.GetAttachmentsByVolumeIdOrTargetPath(ctx, nil, &targetPath)
	assert.NoError(t, err)
	assert.Len(t, *attachments, 1)
	assert.Equal(t, *attachment3, (*attachments)[0])
}

func TestDatabaseWrapper_DeleteAttachment(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	attachment := &PvcAttachment{
		VolumeId:   uuid.New().String(),
		TargetPath: "/test/path",
		Node:       "node1",
		BootID:     "boot1",
		AccessType: "ReadWriteOnce",
	}

	err := db.CreateOrUpdateAttachment(ctx, attachment)
	assert.NoError(t, err)

	err = db.DeleteAttachment(ctx, attachment)
	assert.NoError(t, err)

	var result PvcAttachment
	err = db.First(&result, "volume_id = ?", attachment.VolumeId).Error
	assert.Error(t, err)
	assert.Equal(t, gorm.ErrRecordNotFound, err)
}

func TestDatabaseWrapper_DeleteAttachmentDisregardingAccessType(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	attachment := &PvcAttachment{
		VolumeId:   uuid.New().String(),
		TargetPath: "/test/path",
		Node:       "node1",
		BootID:     "boot1",
		AccessType: "ReadWriteOnce",
	}

	err := db.CreateOrUpdateAttachment(ctx, attachment)
	assert.NoError(t, err)

	err = db.DeleteAttachmentDisregardingAccessType(attachment.VolumeId, attachment.TargetPath, attachment.Node, attachment.BootID)
	assert.NoError(t, err)

	var result PvcAttachment
	err = db.First(&result, "volume_id = ?", attachment.VolumeId).Error
	assert.Error(t, err)
	assert.Equal(t, gorm.ErrRecordNotFound, err)
}
