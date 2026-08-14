package seed

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

const (
	dictVersion = "ecdict-common-2"
	articlesVer = "articles-4"
)

//go:embed data/dict_seed.json
var dictSeedJSON []byte

//go:embed data/articles_seed.json
var articlesSeedJSON []byte

type dictSense struct {
	Pos string `json:"pos"`
	Def string `json:"def"`
}

type dictEntry struct {
	W string      `json:"w"`
	P string      `json:"p"`
	S []dictSense `json:"s"`
}

type seedArticle struct {
	Title      string   `json:"title"`
	Subtitle   string   `json:"subtitle"`
	Level      string   `json:"level"`
	Paragraphs []string `json:"paragraphs"`
}

type seedDataset struct {
	Slug        string        `json:"slug"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Emoji       string        `json:"emoji"`
	Color       string        `json:"color"`
	Articles    []seedArticle `json:"articles"`
}

// Run seeds the built-in ECDICT-derived dictionary and the starter article
// dataset when the database is empty. Both are idempotent through meta flags.
func Run(db *sql.DB) error {
	if err := seedDictionary(db); err != nil {
		return fmt.Errorf("seed dictionary: %w", err)
	}
	if err := seedArticles(db); err != nil {
		return fmt.Errorf("seed articles: %w", err)
	}
	return nil
}

func seeded(db *sql.DB, key, version string) (bool, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v == version, nil
}

func markSeeded(tx *sql.Tx, key, version string) error {
	_, err := tx.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, version)
	return err
}

func seedDictionary(db *sql.DB) error {
	ok, err := seeded(db, "dict_version", dictVersion)
	if err != nil || ok {
		return err
	}

	var entries []dictEntry
	if err := json.Unmarshal(dictSeedJSON, &entries); err != nil {
		return fmt.Errorf("parse dict seed: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("dict seed is empty")
	}

	log.Printf("[seed] importing %d dictionary entries ...", len(entries))
	start := time.Now()

	// Fresh databases benefit from a fast, non-durable bulk import.
	if _, err := db.Exec(`PRAGMA journal_mode=OFF`); err != nil {
		return err
	}
	defer db.Exec(`PRAGMA journal_mode=WAL`)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO dictionary(word, phonetic, senses_json, source)
		VALUES(?, ?, ?, 'ECDICT')`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range entries {
		if e.W == "" {
			continue
		}
		raw, err := json.Marshal(e.S)
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(e.W, e.P, string(raw)); err != nil {
			return fmt.Errorf("insert %q: %w", e.W, err)
		}
	}
	if err := markSeeded(tx, "dict_version", dictVersion); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("[seed] dictionary ready in %s", time.Since(start).Round(time.Millisecond))
	return nil
}

func seedArticles(db *sql.DB) error {
	ok, err := seeded(db, "articles_version", articlesVer)
	if err != nil || ok {
		return err
	}

	var datasets []seedDataset
	if err := json.Unmarshal(articlesSeedJSON, &datasets); err != nil {
		return fmt.Errorf("parse article seed: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Seed content is versioned and read-only from the app's perspective;
	// clear a previous seed before importing the new one.
	if _, err := tx.Exec(`DELETE FROM articles`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM datasets`); err != nil {
		return err
	}

	dsStmt, err := tx.Prepare(`INSERT INTO datasets(slug, title, description, emoji, color)
		VALUES(?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer dsStmt.Close()

	artStmt, err := tx.Prepare(`INSERT INTO articles(dataset_id, title, subtitle, level)
		VALUES(?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer artStmt.Close()

	parStmt, err := tx.Prepare(`INSERT INTO paragraphs(article_id, seq, kind, content)
		VALUES(?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer parStmt.Close()

	for _, ds := range datasets {
		res, err := dsStmt.Exec(ds.Slug, ds.Title, ds.Description, ds.Emoji, ds.Color)
		if err != nil {
			return fmt.Errorf("insert dataset %s: %w", ds.Slug, err)
		}
		datasetID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for _, art := range ds.Articles {
			res, err := artStmt.Exec(datasetID, art.Title, art.Subtitle, art.Level)
			if err != nil {
				return fmt.Errorf("insert article %q: %w", art.Title, err)
			}
			articleID, err := res.LastInsertId()
			if err != nil {
				return err
			}
			seq := 0
			// The article title is also stored as a heading paragraph so the
			// reader has a stable, scrollable TOC anchor.
			if _, err := parStmt.Exec(articleID, seq, "heading", art.Title); err != nil {
				return err
			}
			seq++
			for _, p := range art.Paragraphs {
				if p == "" {
					continue
				}
				if _, err := parStmt.Exec(articleID, seq, "text", p); err != nil {
					return fmt.Errorf("insert paragraph for %q: %w", art.Title, err)
				}
				seq++
			}
		}
	}

	if err := markSeeded(tx, "articles_version", articlesVer); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("[seed] article datasets ready")
	return nil
}
