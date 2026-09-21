// Package content owns article bodies, which live on disk as JSON files rather
// than in the database.
//
// Layout:
//
//	<CONTENT_ROOT>/
//	  aesop/                      ← one directory per dataset
//	    dataset.json              ← dataset metadata (optional but canonical)
//	    the-fox-and-the-grapes.json
//	  news/                       ← extra path depth is allowed
//	    2026-09-21/
//	      000123.json
//
// The files are the source of truth; the database only indexes them. Anything
// that needs article text reads it from here, and the DB rows are derived by
// scanning this tree — never the other way round.
package content

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"english-reading/backend/internal/anchor"
	"english-reading/backend/internal/sentence"
)

// DefaultRoot is used when CONTENT_ROOT is unset. Relative to the process
// working directory, which is backend/ in development.
const DefaultRoot = "data/content"

// EnvRoot is the environment variable that overrides DefaultRoot.
const EnvRoot = "CONTENT_ROOT"

// DatasetFile is the metadata file expected inside every dataset directory.
const DatasetFile = "dataset.json"

// Field limits, mirrored from internal/store/models.go. MySQL runs with
// STRICT_TRANS_TABLES, so an over-long value is an error rather than a silent
// truncation — reject it here, where the file name is still known.
const (
	MaxTitle          = 512
	MaxSubtitle       = 512
	MaxLevel          = 64
	MaxAuthor         = 191
	MaxOrigin         = 512
	MaxRelPath        = 512
	MaxDatasetSlug    = 96
	MaxDatasetTitle   = 191
	MaxDatasetDesc    = 512
	MaxDatasetEmoji   = 16
	MaxDatasetColor   = 32
	MaxParagraphBytes = 60000
)

// Store reads article content from a content root directory.
type Store struct {
	root string
	// realRoot is root with symlinks resolved, used only for containment checks.
	// On macOS /var is a symlink to /private/var, so comparing a resolved path
	// against an unresolved root would report a false escape.
	realRoot string
}

// Open resolves root (empty means "use CONTENT_ROOT, else DefaultRoot") into an
// absolute path. The directory itself is created by EnsureRoot, not here.
func Open(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		root = RootFromEnv()
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve content root %q: %w", root, err)
	}
	s := &Store{root: abs}
	s.refreshRealRoot()
	return s, nil
}

// refreshRealRoot caches the symlink-resolved root. It falls back to the plain
// root while the directory does not exist yet.
func (s *Store) refreshRealRoot() {
	s.realRoot = s.root
	if resolved, err := filepath.EvalSymlinks(s.root); err == nil {
		s.realRoot = resolved
	}
}

// RootFromEnv returns the configured content root: CONTENT_ROOT when set,
// otherwise DefaultRoot. It is the single place that reads the setting.
func RootFromEnv() string {
	if v := strings.TrimSpace(os.Getenv(EnvRoot)); v != "" {
		return v
	}
	return DefaultRoot
}

// Root is the absolute content root directory.
func (s *Store) Root() string { return s.root }

// EnsureRoot creates the content root when it does not exist yet.
func (s *Store) EnsureRoot() error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("create content root %s: %w", s.root, err)
	}
	s.refreshRealRoot()
	return nil
}

// Resolve turns a stored relative path into an absolute path inside the root.
//
// It refuses anything that could escape the root: absolute paths, ".."
// components and symlinks whose target is outside the tree. A relative_path
// value is data — it may come from a hand-edited database row — so it must
// never be able to read an arbitrary file.
func (s *Store) Resolve(relPath string) (string, error) {
	if relPath == "" {
		return "", errors.New("empty relative path")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("relative path %q must not be absolute", relPath)
	}
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("relative path %q escapes the content root", relPath)
	}
	abs := filepath.Join(s.root, clean)
	if !strings.HasPrefix(abs, s.root+string(filepath.Separator)) {
		return "", fmt.Errorf("relative path %q escapes the content root", relPath)
	}
	// A symlink inside the tree must not become a way out of it. Both sides of
	// this comparison are symlink-resolved, so a root that is itself reached
	// through a symlink (macOS /var -> /private/var) does not look like an escape.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		root := s.realRoot
		if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
			return "", fmt.Errorf("relative path %q resolves outside the content root", relPath)
		}
	}
	return abs, nil
}

// ---------------------------------------------------------------------------
// On-disk file shapes
// ---------------------------------------------------------------------------

// DatasetMeta is the parsed dataset.json. It is written by the materialiser and
// read by Scan.
type DatasetMeta struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Emoji       string `json:"emoji,omitempty"`
	Color       string `json:"color,omitempty"`
}

// ArticleFile is the on-disk article document.
//
// Paragraphs is the content; every other field is metadata. ParagraphCount and
// SentenceCounts are optional *declarations*: the scanner recomputes both from
// Paragraphs and rejects the file when a declaration disagrees, which turns a
// stale hand-edited field into a loud error instead of silent anchor drift.
type ArticleFile struct {
	Title          string   `json:"title"`
	Subtitle       string   `json:"subtitle,omitempty"`
	Level          string   `json:"level,omitempty"`
	Author         string   `json:"author,omitempty"`
	Origin         string   `json:"origin,omitempty"`
	PublishedAt    string   `json:"published_at,omitempty"`
	Paragraphs     []string `json:"paragraphs"`
	ParagraphCount *int     `json:"paragraph_count,omitempty"`
	SentenceCounts []int    `json:"sentence_counts,omitempty"`
}

// Article is a loaded, validated article plus everything derived from it.
type Article struct {
	// RelPath is the slash-separated path relative to the content root.
	RelPath string

	Title       string
	Subtitle    string
	Level       string
	Author      string
	Origin      string
	PublishedAt *time.Time

	Paragraphs      []string
	ParagraphHashes []string
	SentenceCounts  []int
	ParagraphCount  int
	SentenceCount   int

	// ContentHash covers the anchorable content: it is derived from the
	// paragraph hashes only, so retitling an article does not invalidate
	// annotations while any text edit does.
	ContentHash string
}

// ParagraphHash is the stable identity of one paragraph; see internal/anchor.
func ParagraphHash(text string) string { return anchor.ParagraphHash(text) }

// SentenceCount is the number of sentences the backend's splitter finds in a
// paragraph. It is the single authority on sentence boundaries: never trust a
// count produced elsewhere (a Python cleaner, a hand edit) as fact.
func SentenceCount(text string) int {
	return len(sentence.Split(text))
}

// ---------------------------------------------------------------------------
// Loading and validation
// ---------------------------------------------------------------------------

// LoadArticle reads and validates one article file. relPath is relative to the
// content root and is stored verbatim on the article row.
func (s *Store) LoadArticle(relPath string) (*Article, error) {
	abs, err := s.Resolve(relPath)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relPath, err)
	}
	return parseArticle(raw, filepath.ToSlash(relPath))
}

// LoadArticleAbs is LoadArticle for a path already known to be inside the root.
func (s *Store) LoadArticleAbs(abs string) (*Article, error) {
	rel, err := filepath.Rel(s.root, abs)
	if err != nil {
		return nil, err
	}
	return s.LoadArticle(filepath.ToSlash(rel))
}

func parseArticle(raw []byte, relPath string) (*Article, error) {
	var f ArticleFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %w", relPath, err)
	}
	art := &Article{
		RelPath:    relPath,
		Title:      strings.TrimSpace(f.Title),
		Subtitle:   strings.TrimSpace(f.Subtitle),
		Level:      strings.TrimSpace(f.Level),
		Author:     strings.TrimSpace(f.Author),
		Origin:     strings.TrimSpace(f.Origin),
		Paragraphs: make([]string, 0, len(f.Paragraphs)),
	}

	if err := checkLen(relPath, "title", art.Title, MaxTitle, true); err != nil {
		return nil, err
	}
	if err := checkLen(relPath, "subtitle", art.Subtitle, MaxSubtitle, false); err != nil {
		return nil, err
	}
	if err := checkLen(relPath, "level", art.Level, MaxLevel, false); err != nil {
		return nil, err
	}
	if err := checkLen(relPath, "author", art.Author, MaxAuthor, false); err != nil {
		return nil, err
	}
	if err := checkLen(relPath, "origin", art.Origin, MaxOrigin, false); err != nil {
		return nil, err
	}
	if err := checkLen(relPath, "relative path", relPath, MaxRelPath, true); err != nil {
		return nil, err
	}

	if strings.TrimSpace(f.PublishedAt) != "" {
		at, err := parseDate(f.PublishedAt)
		if err != nil {
			return nil, fmt.Errorf("%s: published_at %q: %w", relPath, f.PublishedAt, err)
		}
		art.PublishedAt = at
	}

	if len(f.Paragraphs) == 0 {
		return nil, fmt.Errorf("%s: no paragraphs", relPath)
	}
	for i, p := range f.Paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("%s: paragraph %d is empty", relPath, i)
		}
		// The sentence splitter flushes on a line feed, so an embedded newline
		// would manufacture sentence boundaries that no longer match the text
		// the user is reading.
		if strings.ContainsAny(p, "\n\r\t") {
			return nil, fmt.Errorf(
				"%s: paragraph %d contains a newline or tab — re-run the cleaner, "+
					"paragraphs must be single logical blocks", relPath, i)
		}
		if len(p) > MaxParagraphBytes {
			return nil, fmt.Errorf("%s: paragraph %d is %d bytes (limit %d)",
				relPath, i, len(p), MaxParagraphBytes)
		}
		art.Paragraphs = append(art.Paragraphs, p)
		art.ParagraphHashes = append(art.ParagraphHashes, ParagraphHash(p))
		art.SentenceCounts = append(art.SentenceCounts, len(sentence.Split(p)))
	}
	art.ParagraphCount = len(art.Paragraphs)
	art.SentenceCount = sum(art.SentenceCounts)
	art.ContentHash = anchor.ArticleHash(art.ParagraphHashes)

	// Declared metadata is an assertion, not a source of numbers. The Go
	// splitter is the only authority on sentence boundaries; a value computed
	// elsewhere (say, by the Python cleaner) would drift from it silently.
	if f.ParagraphCount != nil && *f.ParagraphCount != art.ParagraphCount {
		return nil, fmt.Errorf("%s: paragraph_count says %d but there are %d paragraphs",
			relPath, *f.ParagraphCount, art.ParagraphCount)
	}
	if len(f.SentenceCounts) > 0 {
		if len(f.SentenceCounts) != art.ParagraphCount {
			return nil, fmt.Errorf("%s: sentence_counts has %d entries but there are %d paragraphs",
				relPath, len(f.SentenceCounts), art.ParagraphCount)
		}
		for i := range f.SentenceCounts {
			if f.SentenceCounts[i] != art.SentenceCounts[i] {
				return nil, fmt.Errorf(
					"%s: sentence_counts[%d] says %d but the splitter finds %d — "+
						"the declaration is stale, delete it or recompute it in Go",
					relPath, i, f.SentenceCounts[i], art.SentenceCounts[i])
			}
		}
	}
	return art, nil
}

// ParagraphByHash returns the text of one paragraph, or false when the hash is
// not part of this article (which is how a stale annotation is detected).
func (a *Article) ParagraphByHash(hash string) (string, bool) {
	for i, h := range a.ParagraphHashes {
		if h == hash {
			return a.Paragraphs[i], true
		}
	}
	return "", false
}

func parseDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			utc := t.UTC()
			return &utc, nil
		}
	}
	return nil, errors.New("expected YYYY-MM-DD, YYYY-MM or RFC3339")
}

func checkLen(relPath, field, value string, limit int, required bool) error {
	if required && value == "" {
		return fmt.Errorf("%s: %s is required", relPath, field)
	}
	if n := utf8.RuneCountInString(value); n > limit {
		return fmt.Errorf("%s: %s is %d chars (limit %d) — MySQL would reject it",
			relPath, field, n, limit)
	}
	return nil
}

func sum(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

// ---------------------------------------------------------------------------
// Scanning
// ---------------------------------------------------------------------------

// ScannedDataset is one dataset directory with its articles.
type ScannedDataset struct {
	Dir      string // directory name relative to the root, used as the dataset key
	Meta     DatasetMeta
	Articles []*Article
}

// Scan walks the content root and returns every dataset with its articles, in a
// stable order. Files and directories whose name starts with "." or "_" are
// skipped, so ".git", editor backups and drafts stay out of the index.
//
// A file that fails validation is collected into bad and skipped rather than
// aborting the walk: one malformed article in a tree of tens of thousands must
// not stop the other 99,999 from being indexed, and the caller reports them all
// at once.
func (s *Store) Scan() (datasets []ScannedDataset, bad []error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("read content root %s: %w", s.root, err)}
	}

	for _, entry := range entries {
		if !entry.IsDir() || skipName(entry.Name()) {
			continue
		}
		ds, errs := s.scanDataset(entry.Name())
		bad = append(bad, errs...)
		if ds == nil {
			continue
		}
		datasets = append(datasets, *ds)
	}
	sort.Slice(datasets, func(i, j int) bool { return datasets[i].Dir < datasets[j].Dir })
	return datasets, bad
}

func (s *Store) scanDataset(dir string) (*ScannedDataset, []error) {
	meta, err := s.loadDatasetMeta(dir)
	if err != nil {
		return nil, []error{err}
	}

	var paths []string
	err = filepath.WalkDir(filepath.Join(s.root, dir), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != filepath.Join(s.root, dir) && skipName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if skipName(d.Name()) || !strings.EqualFold(filepath.Ext(d.Name()), ".json") {
			return nil
		}
		if d.Name() == DatasetFile && filepath.Dir(path) == filepath.Join(s.root, dir) {
			return nil // the dataset metadata file is not an article
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, []error{fmt.Errorf("scan dataset %s: %w", dir, err)}
	}
	sort.Strings(paths)

	ds := &ScannedDataset{Dir: dir, Meta: meta}
	var bad []error
	for _, path := range paths {
		art, err := s.LoadArticleAbs(path)
		if err != nil {
			bad = append(bad, err)
			continue
		}
		ds.Articles = append(ds.Articles, art)
	}
	if len(ds.Articles) == 0 {
		return nil, bad
	}
	return ds, bad
}

// loadDatasetMeta reads dataset.json, falling back to the directory name so a
// hand-made directory still works.
func (s *Store) loadDatasetMeta(dir string) (DatasetMeta, error) {
	meta := DatasetMeta{Slug: dir, Title: dir}
	raw, err := os.ReadFile(filepath.Join(s.root, dir, DatasetFile))
	if err != nil {
		if os.IsNotExist(err) {
			return meta, nil
		}
		return meta, fmt.Errorf("read %s/%s: %w", dir, DatasetFile, err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, fmt.Errorf("%s/%s: invalid JSON: %w", dir, DatasetFile, err)
	}
	if strings.TrimSpace(meta.Slug) == "" {
		meta.Slug = dir
	}
	if strings.TrimSpace(meta.Title) == "" {
		meta.Title = dir
	}
	if meta.Emoji == "" {
		meta.Emoji = "📚"
	}
	if meta.Color == "" {
		meta.Color = "#6366f1"
	}
	if err := checkLen(dir, "dataset slug", meta.Slug, MaxDatasetSlug, true); err != nil {
		return meta, err
	}
	if err := checkLen(dir, "dataset title", meta.Title, MaxDatasetTitle, true); err != nil {
		return meta, err
	}
	if err := checkLen(dir, "dataset description", meta.Description, MaxDatasetDesc, false); err != nil {
		return meta, err
	}
	return meta, nil
}

func skipName(name string) bool {
	return name == "" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}
