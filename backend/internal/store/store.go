package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Open opens (and creates if needed) the SQLite database and applies schema
// migrations. The returned *sql.DB has sane pool settings for SQLite.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite is used through a single process here; keep one writer connection
	// healthy instead of a large pool.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := configure(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func configure(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("apply %q: %w", p, err)
		}
	}
	return nil
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			email         TEXT NOT NULL UNIQUE,
			nickname      TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL,
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token      TEXT PRIMARY KEY,
			user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS verify_codes (
			email      TEXT PRIMARY KEY,
			code       TEXT NOT NULL,
			expires_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS pending_registrations (
			email         TEXT PRIMARY KEY,
			password_hash TEXT NOT NULL,
			nickname      TEXT NOT NULL DEFAULT '',
			code          TEXT NOT NULL,
			expires_at    DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS datasets (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			slug        TEXT NOT NULL UNIQUE,
			title       TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			emoji       TEXT NOT NULL DEFAULT '📚',
			color       TEXT NOT NULL DEFAULT '#6366f1'
		)`,
		`CREATE TABLE IF NOT EXISTS articles (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			dataset_id INTEGER NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
			title      TEXT NOT NULL,
			subtitle   TEXT NOT NULL DEFAULT '',
			level      TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS paragraphs (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			article_id INTEGER NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
			seq        INTEGER NOT NULL,
			kind       TEXT NOT NULL DEFAULT 'text',
			content    TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_paragraphs_article ON paragraphs(article_id, seq)`,
		`CREATE TABLE IF NOT EXISTS dictionary (
			word        TEXT PRIMARY KEY COLLATE NOCASE,
			phonetic    TEXT NOT NULL DEFAULT '',
			senses_json TEXT NOT NULL DEFAULT '[]',
			source      TEXT NOT NULL DEFAULT 'ECDICT'
		)`,
		`CREATE TABLE IF NOT EXISTS word_annotations (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			article_id     INTEGER NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
			paragraph_id   INTEGER NOT NULL REFERENCES paragraphs(id) ON DELETE CASCADE,
			sentence_index INTEGER NOT NULL,
			word_index     INTEGER NOT NULL,
			word           TEXT NOT NULL,
			pos            TEXT NOT NULL DEFAULT '',
			sense          TEXT NOT NULL DEFAULT '',
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, article_id, paragraph_id, sentence_index, word_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_word_annotations_article
			ON word_annotations(user_id, article_id)`,
		`CREATE TABLE IF NOT EXISTS notes (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			article_id     INTEGER NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
			paragraph_id   INTEGER NOT NULL REFERENCES paragraphs(id) ON DELETE CASCADE,
			sentence_index INTEGER NOT NULL DEFAULT -1,
			content        TEXT NOT NULL,
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notes_article ON notes(user_id, article_id)`,
		`CREATE TABLE IF NOT EXISTS translation_cache (
			source_hash    TEXT PRIMARY KEY,
			source_text    TEXT NOT NULL,
			translated_text TEXT NOT NULL,
			target         TEXT NOT NULL DEFAULT 'zh-CN',
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS meta (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS user_translations (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			article_id     INTEGER NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
			paragraph_id   INTEGER NOT NULL REFERENCES paragraphs(id) ON DELETE CASCADE,
			sentence_index INTEGER NOT NULL DEFAULT -1,
			source_text    TEXT NOT NULL,
			translated_text TEXT NOT NULL,
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, article_id, paragraph_id, sentence_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_translations_article
			ON user_translations(user_id, article_id)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w\nstatement: %s", err, q)
		}
	}

	// Drop any legacy rows that no longer make sense (e.g. after schema tweaks
	// in development).
	if _, err := db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`,
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return nil
}
