package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
	_ "modernc.org/sqlite" // Import the modernnc sqlite driver
	"os"
	"path/filepath"
)

const DBPath = "/tmp/csi-wekafs-attachments/csi-attachments.db"

type PvcAttachment struct {
	ID         uint   `gorm:"primaryKey"` // Auto-incrementing ID
	VolumeId   string `gorm:"index:idx_volume_target,unique"`
	TargetPath string `gorm:"index:idx_volume_target,unique"`
	Node       string `json:"node"`
	BootID     string `json:"boot_id"`
	AccessType string `json:"access_type"`
}

type ModerncSQLiteDialector struct {
	DSN    string
	Conn   *sql.DB
	Config *gorm.Config
}

func (dialector ModerncSQLiteDialector) DefaultValueOf(field *schema.Field) clause.Expression {
	//TODO implement me
	panic("implement me")
}

func (dialector ModerncSQLiteDialector) BindVarTo(writer clause.Writer, stmt *gorm.Statement, v interface{}) {
	//TODO implement me
	panic("implement me")
}

func (dialector ModerncSQLiteDialector) QuoteTo(writer clause.Writer, s string) {
	//TODO implement me
	panic("implement me")
}

func (dialector ModerncSQLiteDialector) Explain(sql string, vars ...interface{}) string {
	//TODO implement me
	panic("implement me")
}

func (dialector ModerncSQLiteDialector) Name() string {
	return "sqlite"
}

func (dialector ModerncSQLiteDialector) Initialize(db *gorm.DB) error {
	// Set up the database/sql connection
	if dialector.Conn != nil {
		db.ConnPool = dialector.Conn
	} else {
		sqlDB, err := sql.Open("sqlite", dialector.DSN)
		if err != nil {
			return err
		}
		db.ConnPool = sqlDB
	}

	// Set up GORM configurations
	db.Dialector = dialector
	db.Config = dialector.Config
	return nil
}

func (dialector ModerncSQLiteDialector) Migrator(db *gorm.DB) gorm.Migrator {
	return migrator.Migrator{Config: migrator.Config{DB: db}}
}

func (dialector ModerncSQLiteDialector) DataTypeOf(field *schema.Field) string {
	// Basic SQLite type mapping
	switch field.DataType {
	case schema.Int:
		return "INTEGER"
	case schema.String:
		return "TEXT"
	case schema.Bool:
		return "BOOLEAN"
	default:
		return "BLOB"
	}
}

func (pal *PvcAttachment) String() string {
	return fmt.Sprintf("PVC: %s, Node: %s, TargetPath: %s, BootID: %s, AccessType: %s", pal.VolumeId, pal.Node, pal.TargetPath, pal.BootID, pal.AccessType)
}

func (pal *PvcAttachment) MatchesBootId(bootID string) bool {
	return pal.BootID == bootID
}

func (pal *PvcAttachment) MatchesNode(node string) bool {
	return pal.Node == node
}

func (pal *PvcAttachment) MatchesVolumeId(volumeId string) bool {
	return pal.VolumeId == volumeId
}

func (pal *PvcAttachment) MatchesAccessType(accessType string) bool {
	return pal.AccessType == accessType
}

func (pal *PvcAttachment) MatchesTargetPath(path string) bool {
	return pal.TargetPath == path
}

func (pal *PvcAttachment) Matches(volumeId, path, node, accessType string, bootId string) bool {
	return pal.MatchesVolumeId(volumeId) && pal.MatchesTargetPath(path) && pal.MatchesNode(node) && pal.MatchesBootId(bootId) && pal.MatchesAccessType(accessType)
}

func (pal *PvcAttachment) IsSingleWriter() bool {
	return pal.AccessType == csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER.String() ||
		pal.AccessType == csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER.String()
}

// GetDatabase returns a database for pod attachments that will be used on each node to satisfy the ReadWriteOncePod attachment mode
func GetDatabase(ctx context.Context) (*SqliteDatabase, error) {
	logger := log.Ctx(ctx)
	directory := filepath.Dir(DBPath)
	if err := EnsureDirectoryExists(directory); err != nil {
		logger.Error().Err(err).Msg("Failed to create directory")
		return nil, err
	}

	dsn := "file:" + DBPath
	gormDb, err := gorm.Open(
		ModerncSQLiteDialector{
			DSN: dsn,
		},
		&gorm.Config{
			NamingStrategy: schema.NamingStrategy{
				SingularTable: true,
			},
		},
	)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to connect to the database")
	}

	// Auto-migrate the schema
	if err := gormDb.AutoMigrate(&PvcAttachment{}); err != nil {
		logger.Error().Err(err).Msg("Failed to migrate the database")
	}

	return &SqliteDatabase{
		gormDb,
	}, nil
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

type SqliteDatabase struct {
	*gorm.DB
}

func (d *SqliteDatabase) GetAttachmentsByVolumeIdOrTargetPath(ctx context.Context, volumeId, targetPath *string) (*[]PvcAttachment, error) {
	if d == nil {
		return nil, errors.New("database is nil")
	}
	query := d.Model(&PvcAttachment{})
	if volumeId != nil {
		query = query.Where("volume_id = ?", *volumeId)
	}
	if targetPath != nil {
		query = query.Where("target_path = ?", *targetPath)
	}
	locks := &[]PvcAttachment{}

	err := query.Find(locks).Error
	if err != nil {
		return nil, fmt.Errorf("failed to lookup records: %w", err)
	}

	return locks, nil
}

func (d *SqliteDatabase) CreateAttachment(ctx context.Context, attachment *PvcAttachment) error {
	if d == nil {
		return errors.New("database is nil")
	}
	if err := d.Create(attachment).Error; err != nil {
		return fmt.Errorf("failed to create record: %w", err)
	}
	return nil
}

func (d *SqliteDatabase) UpdateAttachment(ctx context.Context, attachment *PvcAttachment) error {
	if d == nil {
		return errors.New("database is nil")
	}
	existing, err := d.GetAttachmentsByVolumeIdOrTargetPath(ctx, &attachment.VolumeId, &attachment.TargetPath)
	if err != nil {
		return fmt.Errorf("failed to lookup existing record: %w", err)
	}
	if len(*existing) == 0 {
		return errors.New("no record found")
	}
	attachment.ID = (*existing)[0].ID

	if err := d.Save(attachment).Error; err != nil {
		return fmt.Errorf("failed to update record: %w", err)
	}
	return nil
}

func (d *SqliteDatabase) CreateOrUpdateAttachment(ctx context.Context, attachment *PvcAttachment) error {
	if d == nil {
		return errors.New("database is nil")
	}
	existing, err := d.GetAttachmentsByVolumeIdOrTargetPath(ctx, &attachment.VolumeId, &attachment.TargetPath)
	if err != nil {
		return fmt.Errorf("failed to lookup existing record: %w", err)
	}
	if len(*existing) == 0 {
		return d.CreateAttachment(ctx, attachment)
	}
	return d.UpdateAttachment(ctx, attachment)
}

func (d *SqliteDatabase) DeleteAttachment(ctx context.Context, attachment *PvcAttachment) error {
	if d == nil {
		return errors.New("database is nil")
	}
	if err := d.Delete(attachment).Error; err != nil {
		return fmt.Errorf("failed to delete record: %w", err)
	}
	return nil
}

func (d *SqliteDatabase) DeleteAttachmentDisregardingAccessType(volumeId, targetPath, node, bootId string) error {
	// Build the delete query
	result := d.Where("volume_id = ? AND target_path = ? AND node = ? AND boot_id = ?", volumeId, targetPath, node, bootId).Delete(&PvcAttachment{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete record: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("no record found for VolumeId: %s and TargetPath: %s", volumeId, targetPath)
	}
	return nil
}
