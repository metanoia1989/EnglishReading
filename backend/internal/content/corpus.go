package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CorpusDataset is the "cleaned corpus" shape produced by tools/txt2articles.py
// and embedded in the binary as starter content. It is the input to
// MaterializeCorpus, which turns it into the file tree described in the package
// doc.
type CorpusDataset struct {
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Emoji       string          `json:"emoji"`
	Color       string          `json:"color"`
	Articles    []CorpusArticle `json:"articles"`
}

// CorpusArticle is one article inside a corpus file.
type CorpusArticle struct {
	Title       string   `json:"title"`
	Subtitle    string   `json:"subtitle"`
	Level       string   `json:"level"`
	Author      string   `json:"author"`
	Origin      string   `json:"origin"`
	PublishedAt string   `json:"published_at"`
	Paragraphs  []string `json:"paragraphs"`
}

// LoadCorpus parses a corpus JSON document. It accepts either an array of
// datasets or a single dataset object, because both shapes turn up in practice.
func LoadCorpus(raw []byte) ([]CorpusDataset, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		var single CorpusDataset
		if err := json.Unmarshal(raw, &single); err != nil {
			return nil, err
		}
		return []CorpusDataset{single}, nil
	}
	var many []CorpusDataset
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, err
	}
	return many, nil
}

// LoadCorpusFile reads a corpus document from disk.
func LoadCorpusFile(path string) ([]CorpusDataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	datasets, err := LoadCorpus(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return datasets, nil
}

// MaterializeOptions controls how a corpus is written to the tree.
type MaterializeOptions struct {
	// ByDate puts articles under a YYYY-MM-DD directory taken from
	// published_at, when the article has one.
	ByDate bool
	// Overwrite rewrites files that already exist. Without it an existing file
	// is left untouched and reported as skipped, so re-running a materialise
	// cannot silently discard edits made directly in the tree.
	Overwrite bool
	// DryRun reports what would happen without touching the filesystem.
	DryRun bool
}

// MaterializeResult summarises a materialise run.
type MaterializeResult struct {
	Datasets  int
	Written   int
	Skipped   int
	Overwrite int
}

// MaterializeCorpus writes a corpus into the tree, one file per article, and
// returns what it did.
//
// This is the bridge from "cleaned text" to "the content root is the source of
// truth": after it runs, the JSON files — not the corpus document — are what the
// scanner and the reader look at.
func (s *Store) MaterializeCorpus(datasets []CorpusDataset, opts MaterializeOptions) (MaterializeResult, error) {
	var res MaterializeResult
	if err := s.EnsureRoot(); err != nil {
		return res, err
	}

	for _, ds := range datasets {
		dir := Slugify(ds.Slug)
		if dir == "" {
			dir = "dataset"
		}
		res.Datasets++
		if !opts.DryRun {
			if err := s.WriteDatasetMeta(dir, DatasetMeta{
				Slug: ds.Slug, Title: ds.Title, Description: ds.Description,
				Emoji: ds.Emoji, Color: ds.Color,
			}); err != nil {
				return res, err
			}
		}

		for _, ca := range ds.Articles {
			art, err := articleFromCorpus(ca)
			if err != nil {
				return res, fmt.Errorf("dataset %s: %w", ds.Slug, err)
			}
			dateDir := ""
			if opts.ByDate && art.PublishedAt != nil {
				dateDir = art.PublishedAt.Format("2006-01-02")
			}
			art.RelPath = ArticlePathFor(dir, dateDir, art.Title)

			abs, err := s.Resolve(art.RelPath)
			if err != nil {
				return res, err
			}
			if _, statErr := os.Stat(abs); statErr == nil {
				if !opts.Overwrite {
					res.Skipped++
					continue
				}
				res.Overwrite++
			}
			if opts.DryRun {
				res.Written++
				continue
			}
			if _, err := s.WriteArticle(dir, dateDir, art); err != nil {
				return res, err
			}
			res.Written++
		}
	}
	return res, nil
}

// articleFromCorpus validates one corpus article and derives its content
// metadata (hashes, counts) exactly the way a scan would.
func articleFromCorpus(ca CorpusArticle) (*Article, error) {
	is := ArticleFile{
		Title:       ca.Title,
		Subtitle:    ca.Subtitle,
		Level:       ca.Level,
		Author:      ca.Author,
		Origin:      ca.Origin,
		PublishedAt: ca.PublishedAt,
		Paragraphs:  ca.Paragraphs,
	}
	// Reuse the article parser so a corpus is held to the same rules as a file
	// edited in the tree — no second, laxer validation path.
	raw, err := json.Marshal(is)
	if err != nil {
		return nil, err
	}
	art, err := parseArticle(raw, "corpus:"+ca.Title)
	if err != nil {
		return nil, err
	}
	return art, nil
}

// CorpusPaths lists the relative paths a corpus would be materialised to,
// without touching the filesystem.
func CorpusPaths(datasets []CorpusDataset, byDate bool) []string {
	var out []string
	for _, ds := range datasets {
		dir := Slugify(ds.Slug)
		for _, ca := range ds.Articles {
			dateDir := ""
			if byDate && strings.TrimSpace(ca.PublishedAt) != "" {
				dateDir = strings.TrimSpace(ca.PublishedAt)
				if len(dateDir) > 10 {
					dateDir = dateDir[:10]
				}
			}
			out = append(out, filepath.ToSlash(ArticlePathFor(dir, dateDir, ca.Title)))
		}
	}
	sort.Strings(out)
	return out
}
