package seed

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"english-reading/backend/internal/store"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := store.Open(store.Config{
		Driver: store.DriverSQLite,
		DSN:    filepath.Join(t.TempDir(), "seed.db"),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db
}

func openMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	db, err := store.Open(store.Config{Driver: store.DriverMySQL, DSN: dsn})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	// Start from an empty schema so the seed flags are absent.
	for i := len(store.AllModels()) - 1; i >= 0; i-- {
		_ = db.Migrator().DropTable(store.AllModels()[i])
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func engines(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	m := map[string]*gorm.DB{"sqlite": openTestDB(t)}
	if os.Getenv("TEST_MYSQL_DSN") != "" {
		m["mysql"] = openMySQL(t)
	}
	return m
}

func TestSeedImportsEverything(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full dictionary import in -short mode")
	}
	for name, db := range engines(t) {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			if err := Run(db); err != nil {
				t.Fatalf("seed: %v", err)
			}
			elapsed := time.Since(start)
			t.Logf("seed completed in %s", elapsed.Round(time.Millisecond))

			var dict int64
			db.Model(&store.Dictionary{}).Count(&dict)
			if dict != 89501 {
				t.Errorf("dictionary rows = %d, want 89501", dict)
			}

			var datasets, articles, paragraphs int64
			db.Model(&store.Dataset{}).Count(&datasets)
			db.Model(&store.Article{}).Count(&articles)
			db.Model(&store.Paragraph{}).Count(&paragraphs)
			if datasets != 3 {
				t.Errorf("datasets = %d, want 3", datasets)
			}
			if articles != 10 {
				t.Errorf("articles = %d, want 10", articles)
			}
			if paragraphs == 0 {
				t.Error("no paragraphs imported")
			}

			// The heading paragraph of each article must exist for the TOC.
			var headings int64
			db.Model(&store.Paragraph{}).Where("kind = ?", "heading").Count(&headings)
			if headings != articles {
				t.Errorf("heading paragraphs = %d, want one per article (%d)", headings, articles)
			}
		})
	}
}

// Re-running must not duplicate anything: the version flags gate the import.
func TestSeedIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full dictionary import in -short mode")
	}
	for name, db := range engines(t) {
		t.Run(name, func(t *testing.T) {
			if err := Run(db); err != nil {
				t.Fatalf("first seed: %v", err)
			}
			var before int64
			db.Model(&store.Dictionary{}).Count(&before)

			if err := Run(db); err != nil {
				t.Fatalf("second seed: %v", err)
			}
			var after int64
			db.Model(&store.Dictionary{}).Count(&after)
			if after != before {
				t.Errorf("dictionary grew on re-seed: %d -> %d", before, after)
			}

			var articles int64
			db.Model(&store.Article{}).Count(&articles)
			if articles != 10 {
				t.Errorf("articles after re-seed = %d, want 10", articles)
			}
		})
	}
}

// The meta upsert must update in place rather than error on the second write.
func TestMarkSeededUpserts(t *testing.T) {
	db := openTestDB(t)
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := markSeeded(tx, "k", "v1"); err != nil {
			return err
		}
		return markSeeded(tx, "k", "v2")
	}); err != nil {
		t.Fatalf("markSeeded: %v", err)
	}
	var rows int64
	db.Model(&store.Meta{}).Where("`key` = ?", "k").Count(&rows)
	if rows != 1 {
		t.Errorf("meta rows = %d, want 1", rows)
	}
	var m store.Meta
	db.Where("`key` = ?", "k").Take(&m)
	if m.Value != "v2" {
		t.Errorf("meta value = %q, want v2", m.Value)
	}
}
