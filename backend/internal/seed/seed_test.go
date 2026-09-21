package seed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"english-reading/backend/internal/content"
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

			// Articles are no longer seeded into the database: their bodies
			// live in the content tree and are indexed by content.Sync. The
			// dictionary is all this package still writes.
			var datasets, articles int64
			db.Model(&store.Dataset{}).Count(&datasets)
			db.Model(&store.Article{}).Count(&articles)
			if datasets != 0 || articles != 0 {
				t.Errorf("seed must not write articles: datasets=%d articles=%d", datasets, articles)
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
			if articles != 0 {
				t.Errorf("articles after re-seed = %d, want 0 (bodies live in files)", articles)
			}
		})
	}
}

// The starter corpus must land in an empty tree, and must never touch a tree
// that already has content — the tree is the source of truth after first run.
func TestMaterializeStarterRespectsExistingContent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full dictionary import in -short mode")
	}
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	root := t.TempDir()
	cs, err := content.Open(root)
	if err != nil {
		t.Fatalf("open content: %v", err)
	}

	// Empty tree: the starter is written.
	if err := MaterializeStarter(db, cs); err != nil {
		t.Fatalf("materialize starter: %v", err)
	}
	datasets, bad := cs.Scan()
	if len(bad) > 0 {
		t.Fatalf("scan reported %d bad file(s): %v", len(bad), bad[0])
	}
	total := 0
	for _, ds := range datasets {
		total += len(ds.Articles)
	}
	if total == 0 {
		t.Fatal("starter corpus produced no article files")
	}

	// A tree with content is left alone, even when a file was edited by hand.
	first := datasets[0].Articles[0]
	abs, err := cs.Resolve(first.RelPath)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	edited := strings.Replace(string(mustRead(t, abs)), first.Paragraphs[0], "EDITED BY HAND", 1)
	if err := os.WriteFile(abs, []byte(edited), 0o644); err != nil {
		t.Fatalf("edit file: %v", err)
	}
	if err := MaterializeStarter(db, cs); err != nil {
		t.Fatalf("second materialize: %v", err)
	}
	if got := string(mustRead(t, abs)); !strings.Contains(got, "EDITED BY HAND") {
		t.Error("existing content was overwritten by the starter corpus")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
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
