package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"english-reading/backend/internal/anchor"
)

// ArticlePathFor builds the relative path an article should live at:
//
//	<dataset-dir>[/<date>]/<slug>.json
//
// The file name is derived from the title but is meant to be treated as
// immutable once written: renaming a file changes relative_path, and the rows
// that point at the old name must be re-synced. Titles in the database are the
// authority for display, so a title fix does not require a rename.
func ArticlePathFor(datasetDir, dateDir, title string) string {
	name := Slugify(title)
	if name == "" {
		name = "article"
	}
	parts := []string{datasetDir}
	if dateDir != "" {
		parts = append(parts, dateDir)
	}
	parts = append(parts, name+".json")
	return filepath.ToSlash(filepath.Join(parts...))
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns an article title into a filename-safe slug.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugStrip.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 80 {
		s = strings.Trim(s[:80], "-")
	}
	return s
}

// WriteArticle materialises one article file. It returns the relative path
// written.
//
// The document records paragraph_count and sentence_counts so the tree is
// self-describing: a scan recomputes both and refuses a file whose declaration
// disagrees, which turns a hand edit that forgot to update the counts into a
// loud error instead of silent anchor drift.
//
// The article-level content_hash is deliberately NOT written here. It is
// derived from the paragraph hashes and lives in the database, where the sync
// compares it against a freshly computed value; storing a copy in the file
// would add a second thing that can go stale.
func (s *Store) WriteArticle(datasetDir, dateDir string, art *Article) (string, error) {
	relPath := art.RelPath
	if relPath == "" {
		relPath = ArticlePathFor(datasetDir, dateDir, art.Title)
	}
	abs, err := s.Resolve(relPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}

	count := art.ParagraphCount
	doc := ArticleFile{
		Title:          art.Title,
		Subtitle:       art.Subtitle,
		Level:          art.Level,
		Author:         art.Author,
		Origin:         art.Origin,
		Paragraphs:     art.Paragraphs,
		ParagraphCount: &count,
		SentenceCounts: art.SentenceCounts,
	}
	if art.PublishedAt != nil {
		doc.PublishedAt = art.PublishedAt.Format("2006-01-02")
	}

	raw, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", relPath, err)
	}
	return relPath, nil
}

// WriteDatasetMeta writes (or refreshes) <dir>/dataset.json.
func (s *Store) WriteDatasetMeta(dir string, meta DatasetMeta) error {
	if meta.Slug == "" {
		meta.Slug = dir
	}
	if meta.Title == "" {
		meta.Title = dir
	}
	if meta.Emoji == "" {
		meta.Emoji = "📚"
	}
	if meta.Color == "" {
		meta.Color = "#6366f1"
	}
	abs := filepath.Join(s.root, dir, DatasetFile)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(meta, "", " ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(abs, raw, 0o644)
}

// HasArticles reports whether the content root already holds at least one
// article file. Used to decide whether a fresh install needs the starter
// corpus materialised.
//
// This is a cheap existence probe on purpose. Going through Scan would parse
// and validate every file, so one malformed article anywhere in the tree could
// stop the server from booting.
func (s *Store) HasArticles() (bool, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read content root %s: %w", s.root, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || skipName(entry.Name()) {
			continue
		}
		found, err := s.containsArticleFile(entry.Name())
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

// containsArticleFile reports whether one dataset directory holds any article
// JSON at all, stopping at the first hit.
func (s *Store) containsArticleFile(dir string) (bool, error) {
	found := false
	root := filepath.Join(s.root, dir)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if skipName(d.Name()) || !strings.EqualFold(filepath.Ext(d.Name()), ".json") {
			return nil
		}
		if d.Name() == DatasetFile && filepath.Dir(path) == root {
			return nil
		}
		found = true
		// Stop walking this dataset; one hit is all the caller needs.
		return filepath.SkipDir
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

// NewArticle builds an Article from plain text paragraphs, computing every
// derived field. Callers that already have a file use LoadArticle instead.
func NewArticle(relPath, title string, paragraphs []string) *Article {
	art := &Article{RelPath: relPath, Title: title}
	for _, p := range paragraphs {
		art.Paragraphs = append(art.Paragraphs, strings.TrimSpace(p))
	}
	art.Refresh()
	return art
}

// Refresh recomputes every derived field from Paragraphs. Call it after
// mutating Paragraphs directly.
func (a *Article) Refresh() {
	a.ParagraphHashes = a.ParagraphHashes[:0]
	a.SentenceCounts = a.SentenceCounts[:0]
	a.SentenceCount = 0
	for _, p := range a.Paragraphs {
		a.ParagraphHashes = append(a.ParagraphHashes, ParagraphHash(p))
		n := SentenceCount(p)
		a.SentenceCounts = append(a.SentenceCounts, n)
		a.SentenceCount += n
	}
	a.ParagraphCount = len(a.Paragraphs)
	a.ContentHash = anchor.ArticleHash(a.ParagraphHashes)
}
