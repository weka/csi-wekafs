package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/rs/zerolog/log"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // Import the modernc sqlite driver
)

const DBPath = "/tmp/csi-wekafs-attachments/csi-attachments.db"

type PvcAttachment struct {
	ID         int64
	VolumeId   string
	TargetPath string
	Node       string
	BootID     string
	AccessType string
}

func (pal *PvcAttachment) Matches(volumeId, path, node, accessType, bootId string) bool {
	return pal.VolumeId == volumeId &&
		pal.TargetPath == path &&
		pal.Node == node &&
		pal.AccessType == accessType &&
		pal.BootID == bootId
}

func (pal *PvcAttachment) IsSingleWriter() bool {
	return pal.AccessType == csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER.String() ||
		pal.AccessType == csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER.String()
}

type SqliteDatabase struct {
	db *sql.DB
}

func EnsureDirectoryExists(directory string) error {
	_, err := os.Stat(directory)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(directory, 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
		} else {
			return fmt.Errorf("failed to stat directory: %w", err)
		}
	}
	return nil
}

func GetDatabase(ctx context.Context) (*SqliteDatabase, error) {
	logger := log.Ctx(ctx)
	directory := filepath.Dir(DBPath)
	if err := EnsureDirectoryExists(directory); err != nil {
		logger.Error().Err(err).Msg("Failed to create directory")
		return nil, err
	}

	dsn := "file:" + DBPath
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to connect to the database")
		return nil, err
	}

	// Ensure table creation
	query := `
	CREATE TABLE IF NOT EXISTS pvc_attachments (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		volume_id TEXT NOT NULL,
		target_path TEXT NOT NULL,
		node TEXT,
		boot_id TEXT,
		access_type TEXT,
		UNIQUE(volume_id, target_path)
	);`
	if _, err := db.Exec(query); err != nil {
		logger.Error().Err(err).Msg("Failed to create table")
		return nil, err
	}

	return &SqliteDatabase{db: db}, nil
}

func (d *SqliteDatabase) CreateAttachment(ctx context.Context, attachment *PvcAttachment) error {
	query := `
	INSERT INTO pvc_attachments (volume_id, target_path, node, boot_id, access_type)
	VALUES (?, ?, ?, ?, ?);
	`
	result, err := d.db.ExecContext(ctx, query, attachment.VolumeId, attachment.TargetPath, attachment.Node, attachment.BootID, attachment.AccessType)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return errors.New("attachment already exists")
		}
		return fmt.Errorf("failed to create attachment: %w", err)
	}
	attachment.ID, _ = result.LastInsertId()
	return nil
}

func (d *SqliteDatabase) GetAttachments(ctx context.Context, volumeId, targetPath *string) ([]PvcAttachment, error) {
	query := `
	SELECT id, volume_id, target_path, node, boot_id, access_type
	FROM pvc_attachments
	WHERE (? IS NULL OR volume_id = ?) AND (? IS NULL OR target_path = ?);
	`
	rows, err := d.db.QueryContext(ctx, query, volumeId, volumeId, targetPath, targetPath)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch attachments: %w", err)
	}
	defer rows.Close()

	var attachments []PvcAttachment
	for rows.Next() {
		var attachment PvcAttachment
		if err := rows.Scan(&attachment.ID, &attachment.VolumeId, &attachment.TargetPath, &attachment.Node, &attachment.BootID, &attachment.AccessType); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		attachments = append(attachments, attachment)
	}
	return attachments, nil
}

func (d *SqliteDatabase) UpdateAttachment(ctx context.Context, attachment *PvcAttachment) error {
	query := `
	UPDATE pvc_attachments
	SET node = ?, boot_id = ?, access_type = ?
	WHERE volume_id = ? AND target_path = ?;
	`
	result, err := d.db.ExecContext(ctx, query, attachment.Node, attachment.BootID, attachment.AccessType, attachment.VolumeId, attachment.TargetPath)
	if err != nil {
		return fmt.Errorf("failed to update attachment: %w", err)
	}
	if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
		return errors.New("no matching record found")
	}
	return nil
}

func (d *SqliteDatabase) CreateOrUpdateAttachment(ctx context.Context, attachment *PvcAttachment) error {
	// First, check if the record exists
	query := `
	SELECT id 
	FROM pvc_attachments 
	WHERE volume_id = ? AND target_path = ?;
	`
	var existingID int64
	err := d.db.QueryRowContext(ctx, query, attachment.VolumeId, attachment.TargetPath).Scan(&existingID)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// If no rows are found, create a new attachment
			return d.CreateAttachment(ctx, attachment)
		}
		return fmt.Errorf("failed to check existing attachment: %w", err)
	}

	// If the record exists, update it
	attachment.ID = existingID
	updateQuery := `
	UPDATE pvc_attachments 
	SET node = ?, boot_id = ?, access_type = ? 
	WHERE id = ?;
	`
	_, err = d.db.ExecContext(ctx, updateQuery, attachment.Node, attachment.BootID, attachment.AccessType, attachment.ID)
	if err != nil {
		return fmt.Errorf("failed to update attachment: %w", err)
	}

	return nil
}

func (d *SqliteDatabase) DeleteAttachment(ctx context.Context, attachment *PvcAttachment) error {
	query := `
	DELETE FROM pvc_attachments
	WHERE volume_id = ? AND target_path = ?;
	`
	result, err := d.db.ExecContext(ctx, query, attachment.VolumeId, attachment.TargetPath)
	if err != nil {
		return fmt.Errorf("failed to delete attachment: %w", err)
	}
	if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
		return errors.New("no matching record found")
	}
	return nil
}

func (d *SqliteDatabase) DeleteByAttributes(ctx context.Context, volumeId, targetPath, node, bootId string) error {
	query := `
	DELETE FROM pvc_attachments
	WHERE volume_id = ? AND target_path = ? AND node = ? AND boot_id = ?;
	`
	result, err := d.db.ExecContext(ctx, query, volumeId, targetPath, node, bootId)
	if err != nil {
		return fmt.Errorf("failed to delete attachment: %w", err)
	}
	if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
		return fmt.Errorf("no matching record found")
	}
	return nil
}

func (d *SqliteDatabase) GetAttachmentsByVolumeIdOrTargetPath(ctx context.Context, volumeId, targetPath *string) (*[]PvcAttachment, error) {
	var (
		query  strings.Builder
		args   []interface{}
		result []PvcAttachment
	)

	// Base query
	query.WriteString(`
		SELECT id, volume_id, target_path, node, boot_id, access_type
		FROM pvc_attachments
		WHERE 1=1
	`)

	// Dynamically add conditions based on input parameters
	if volumeId != nil {
		query.WriteString(" AND volume_id = ?")
		args = append(args, *volumeId)
	}
	if targetPath != nil {
		query.WriteString(" AND target_path = ?")
		args = append(args, *targetPath)
	}

	// Execute the query
	rows, err := d.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query attachments: %w", err)
	}
	defer rows.Close()

	// Iterate through rows and map to PvcAttachment structs
	for rows.Next() {
		var attachment PvcAttachment
		if err := rows.Scan(&attachment.ID, &attachment.VolumeId, &attachment.TargetPath, &attachment.Node, &attachment.BootID, &attachment.AccessType); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		result = append(result, attachment)
	}

	// Check for iteration errors
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error during row iteration: %w", err)
	}

	return &result, nil
}
