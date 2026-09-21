package store

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Migrate applies the schema derived from the models in models.go. It is
// idempotent: GORM inspects the live schema and only creates what is missing.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(AllModels()...); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// Expired sessions are dropped on every boot, as before.
	if err := db.Where("expires_at <= ?", time.Now().UTC()).
		Delete(&Session{}).Error; err != nil {
		return fmt.Errorf("prune sessions: %w", err)
	}
	return nil
}
