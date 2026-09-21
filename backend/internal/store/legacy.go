package store

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"gorm.io/gorm"

	"english-reading/backend/internal/anchor"
)

// This file holds the one-time migration away from the paragraph table.
//
// Before: article bodies lived in `paragraphs`, and annotations anchored to
// `paragraph_id`, the paragraph's auto-increment row id.
// After: bodies live in JSON files under CONTENT_ROOT, and annotations anchor to
// `paragraph_hash`, a sha256 of the paragraph text.
//
// Three things have to happen, in this order:
//
//  1. preMigrateLegacy — give the new UNIQUE columns (datasets.dir,
//     articles.rel_path) a distinct value per existing row, otherwise
//     AutoMigrate cannot create the unique indexes over a table full of "".
//  2. AutoMigrate — add every new column, including the NOT NULL paragraph_hash
//     on the anchor tables (existing rows get "").
//  3. migrateLegacyParagraphs — translate paragraph_id into paragraph_hash, then
//     rebuild the anchor tables and drop `paragraphs`.
//
// Step 3 rebuilds rather than ALTERs on purpose. MySQL refuses to drop a column
// that participates in a foreign key, and the old composite unique indexes
// contain paragraph_id, so surgical ALTERs would need engine-specific
// constraint introspection. Dropping and recreating three small tables is
// engine-agnostic — and the rows are streamed to a JSON backup file first.

// anchorTables are the three tables that carried paragraph_id.
var anchorTables = []string{"word_annotations", "notes", "user_translations"}

// backupFilePrefix names the safety copy written before the anchor tables are
// rebuilt.
const backupFilePrefix = "legacy-anchor-backup-"

// preMigrateLegacy prepares existing tables so AutoMigrate can add its unique
// indexes without tripping over duplicate empty strings.
func preMigrateLegacy(db *gorm.DB) error {
	m := db.Migrator()

	// datasets.dir is UNIQUE. Legacy rows have no dir, and MySQL/SQLite would
	// give them all "" — backfill from the already-unique slug first.
	if m.HasTable(&Dataset{}) && !m.HasColumn(&Dataset{}, "dir") {
		if err := m.AddColumn(&Dataset{}, "Dir"); err != nil {
			return fmt.Errorf("add datasets.dir: %w", err)
		}
		if err := db.Exec("UPDATE datasets SET dir = slug").Error; err != nil {
			return fmt.Errorf("backfill datasets.dir: %w", err)
		}
		log.Printf("[migrate] datasets.dir added and filled from slug")
	}

	// articles.rel_path is UNIQUE too. Legacy articles live in `paragraphs` and
	// have no file yet, so give each row a placeholder that no real file can
	// occupy — Scan skips directories whose name starts with "_". The content
	// sync replaces these with real paths, matching articles by title.
	if m.HasTable(&Article{}) && !m.HasColumn(&Article{}, "rel_path") {
		if err := m.AddColumn(&Article{}, "RelPath"); err != nil {
			return fmt.Errorf("add articles.rel_path: %w", err)
		}
		var ids []int64
		if err := db.Model(&Article{}).Order("id").Pluck("id", &ids).Error; err != nil {
			return fmt.Errorf("list articles for rel_path backfill: %w", err)
		}
		for _, id := range ids {
			// One UPDATE per row keeps this engine-agnostic: string
			// concatenation differs between SQLite (||) and MySQL (CONCAT).
			//
			// UpdateColumn, not Update: this runs before AutoMigrate, so the
			// auto-maintained updated_at column does not exist yet and GORM's
			// Update would try to write it.
			if err := db.Table("articles").Where("id = ?", id).
				UpdateColumn("rel_path", fmt.Sprintf("__legacy__/%d", id)).Error; err != nil {
				return fmt.Errorf("backfill articles.rel_path for id %d: %w", id, err)
			}
		}
		log.Printf("[migrate] articles.rel_path added; %d row(s) given placeholder paths", len(ids))
	}

	return nil
}

// migrateLegacyParagraphs converts paragraph_id anchors into paragraph_hash
// anchors and removes the paragraph store. It is a no-op once the paragraphs
// table is gone, so it is safe to call on every boot.
func migrateLegacyParagraphs(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("paragraphs") {
		return nil
	}

	hasLegacyColumn := false
	for _, model := range []any{&WordAnnotation{}, &Note{}, &UserTranslation{}} {
		if m.HasColumn(model, "paragraph_id") {
			hasLegacyColumn = true
			break
		}
	}
	if !hasLegacyColumn {
		// Paragraphs table left over without any referencing column: drop it.
		log.Printf("[migrate] dropping orphaned paragraphs table")
		return m.DropTable("paragraphs")
	}

	log.Printf("[migrate] moving annotations from paragraph_id to paragraph_hash ...")

	// 1. Read every anchor row as a raw map: the legacy paragraph_id column is
	//    not part of the models any more, so the rows cannot be scanned into
	//    them.
	type tableRows struct {
		table string
		model any
		rows  []map[string]any
	}
	tables := []tableRows{
		{"word_annotations", &WordAnnotation{}, nil},
		{"notes", &Note{}, nil},
		{"user_translations", &UserTranslation{}, nil},
	}
	total := 0
	for i := range tables {
		if err := db.Table(tables[i].table).Find(&tables[i].rows).Error; err != nil {
			return fmt.Errorf("read %s: %w", tables[i].table, err)
		}
		total += len(tables[i].rows)
	}

	// 2. Safety copy. The tables are dropped in step 4 and DDL cannot be rolled
	//    back inside a transaction on MySQL, so the rows go to disk first.
	if total > 0 {
		path, err := writeAnchorBackup(tables[0].rows, tables[1].rows, tables[2].rows)
		if err != nil {
			return fmt.Errorf("write anchor backup: %w", err)
		}
		log.Printf("[migrate] %d anchor row(s) backed up to %s", total, path)
	}

	// 3. paragraph_id -> paragraph_hash. Hashing happens in Go so both engines
	//    agree; the paragraph text is read in batches.
	hashes := map[int64]string{}
	var lastID int64
	const batch = 500
	for {
		var rows []struct {
			ID      int64
			Content string
		}
		if err := db.Table("paragraphs").Select("id, content").
			Where("id > ?", lastID).Order("id").Limit(batch).
			Scan(&rows).Error; err != nil {
			return fmt.Errorf("read paragraphs: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			hashes[row.ID] = anchor.ParagraphHash(row.Content)
			lastID = row.ID
		}
	}

	// 4. Drop the three anchor tables and let AutoMigrate recreate them with
	//    the new shape (this also drops the legacy foreign keys, which MySQL
	//    would otherwise refuse to let us alter).
	for _, t := range tables {
		if err := m.DropTable(t.table); err != nil {
			return fmt.Errorf("drop %s: %w", t.table, err)
		}
	}
	if err := db.AutoMigrate(&WordAnnotation{}, &Note{}, &UserTranslation{}); err != nil {
		return fmt.Errorf("recreate anchor tables: %w", err)
	}

	// 5. Re-insert, preserving primary keys so clients holding an annotation id
	//    keep working. Rows whose paragraph vanished are dropped, because their
	//    anchor no longer refers to any text.
	restored := 0
	orphaned := 0
	for i := range tables {
		for _, row := range tables[i].rows {
			pid := asInt64(row["paragraph_id"])
			hash, ok := hashes[pid]
			if !ok {
				orphaned++
				continue
			}
			row["paragraph_hash"] = hash
			delete(row, "paragraph_id")
			if err := insertLegacyRow(db, tables[i].table, row); err != nil {
				return fmt.Errorf("restore %s row %v: %w", tables[i].table, row["id"], err)
			}
			restored++
		}
	}

	// 6. The bodies now live in files.
	if err := m.DropTable("paragraphs"); err != nil {
		return fmt.Errorf("drop paragraphs: %w", err)
	}
	log.Printf("[migrate] anchors migrated: %d restored, %d dropped (paragraph no longer exists)",
		restored, orphaned)
	return nil
}

// insertLegacyRow writes one legacy map row back through the given table,
// keeping only the columns the new schema still has.
func insertLegacyRow(db *gorm.DB, table string, row map[string]any) error {
	var keep []string
	switch table {
	case "word_annotations":
		keep = []string{"id", "user_id", "article_id", "paragraph_hash",
			"sentence_index", "word_index", "word", "pos", "sense"}
	case "notes":
		keep = []string{"id", "user_id", "article_id", "paragraph_hash",
			"sentence_index", "content"}
	case "user_translations":
		keep = []string{"id", "user_id", "article_id", "paragraph_hash",
			"sentence_index", "source_text", "translated_text"}
	default:
		return fmt.Errorf("unknown table %q", table)
	}

	values := map[string]any{}
	for _, k := range keep {
		if v, ok := row[k]; ok && v != nil {
			values[k] = v
		}
	}
	// Timestamps are re-derived rather than copied. A driver may hand back a
	// datetime as a string, and MySQL in STRICT_TRANS_TABLES rejects anything
	// that is not a plain time value (see AGENTS §4.5) — so a copied string
	// could abort the whole migration. Original values stay in the backup file.
	now := time.Now().UTC()
	values["created_at"] = firstTime(row["created_at"], now)
	values["updated_at"] = firstTime(row["updated_at"], now)
	return db.Table(table).Create(values).Error
}

// firstTime coerces a scanned column into a time.Time, falling back to def.
func firstTime(v any, def time.Time) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t.UTC()
	case string:
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05", "2006-01-02",
		} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed.UTC()
			}
		}
	case []byte:
		return firstTime(string(t), def)
	}
	return def
}

func writeAnchorBackup(groups ...[]map[string]any) (string, error) {
	doc := map[string]any{}
	for i, name := range anchorTables {
		doc[name] = groups[i]
	}
	raw, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s%d.json", backupFilePrefix, time.Now().Unix())
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, raw, 0o600); err != nil {
		return "", err
	}
	return abs, nil
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	case []byte:
		var out int64
		_, _ = fmt.Sscan(string(n), &out)
		return out
	case string:
		var out int64
		_, _ = fmt.Sscan(n, &out)
		return out
	default:
		return 0
	}
}
