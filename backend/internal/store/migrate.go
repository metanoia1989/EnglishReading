package store

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Migrate applies the schema derived from the models in models.go. It is
// idempotent: GORM inspects the live schema and only creates what is missing.
//
// Article bodies moved out of the database into JSON files under CONTENT_ROOT,
// so this also drives the one-time migration that turns paragraph row ids into
// content hashes on the three anchor tables. See legacy.go.
func Migrate(db *gorm.DB) error {
	if err := preMigrateLegacy(db); err != nil {
		return fmt.Errorf("migrate (pre): %w", err)
	}
	if err := db.AutoMigrate(AllModels()...); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := migrateLegacyParagraphs(db); err != nil {
		return fmt.Errorf("migrate paragraphs: %w", err)
	}
	// Expired sessions are dropped on every boot, as before.
	if err := db.Where("expires_at <= ?", time.Now().UTC()).
		Delete(&Session{}).Error; err != nil {
		return fmt.Errorf("prune sessions: %w", err)
	}
	return nil
}
