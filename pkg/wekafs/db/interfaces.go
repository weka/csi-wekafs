package db

import "context"

type Database interface {
	GetAttachmentsByVolumeIdOrTargetPath(ctx context.Context, volumeId, targetPath *string) (*[]PvcAttachment, error)
	CreateAttachment(ctx context.Context, attachment *PvcAttachment) error
	UpdateAttachment(ctx context.Context, attachment *PvcAttachment) error
	CreateOrUpdateAttachment(ctx context.Context, attachment *PvcAttachment) error
	DeleteAttachment(ctx context.Context, attachment *PvcAttachment) error
	DeleteAttachmentDisregardingAccessType(volumeId, targetPath, node, bootId string) error
}
