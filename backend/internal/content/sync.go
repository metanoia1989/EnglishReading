package content

import (
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"english-reading/backend/internal/store"
)

// This file syncs the database index with the file tree. The tree is the source
// of truth: this direction only, never the reverse.
//
// Two properties matter more than speed here.
//
//   - Article identity is preserved. A row is matched by relative_path first and
//     by (dataset, title) second, so renaming a file or fixing a title updates
//     the existing row instead of creating a new one. Because annotations hang
//     off article_id, that is what keeps them alive across a reorganised tree.
//
//   - Content changes are reported, never punished. When a file's body changes,
//     the article's content_hash changes and the annotations anchored to the
//     paragraphs that moved are already unreachable by hash. Sync does NOT
//     delete them: it counts them and says so, leaving the decision to a human.

// SyncOptions controls a sync run.
type SyncOptions struct {
	// Prune deletes index rows whose file has disappeared, which cascades their
	// annotations away. Without it those rows are only flagged Missing.
	Prune bool
	// DryRun reports what would change without writing.
	DryRun bool
}

// SyncStats summarises a sync run.
type SyncStats struct {
	DatasetsCreated int
	DatasetsUpdated int
	ArticlesCreated int
	ArticlesUpdated int
	ArticlesAdopted int // matched by title after their file moved
	ArticlesKept    int // identical content and metadata
	ArticlesChanged int // body changed: anchors on this article may be stale
	ArticlesMissing int
	ArticlesPruned  int
	// BadFiles counts files that failed validation and were skipped. They are
	// left on disk untouched so a human can fix them.
	BadFiles int
}

// Sync brings the database index in line with the content tree.
func Sync(db *gorm.DB, s *Store, opts SyncOptions) (SyncStats, error) {
	var st SyncStats

	datasets, bad := s.Scan()
	// Malformed files are reported, not fatal: everything else still syncs.
	for _, err := range bad {
		log.Printf("[sync] ! skipped: %v", err)
	}
	st.BadFiles = len(bad)

	seenArticles := map[int64]bool{}

	for _, scanned := range datasets {
		ds, err := syncDataset(db, scanned, opts, &st)
		if err != nil {
			return st, fmt.Errorf("dataset %s: %w", scanned.Dir, err)
		}
		for _, art := range scanned.Articles {
			id, err := syncArticle(db, ds.ID, art, opts, &st)
			if err != nil {
				return st, fmt.Errorf("article %s: %w", art.RelPath, err)
			}
			if id != 0 {
				seenArticles[id] = true
			}
		}
	}

	if err := markMissing(db, seenArticles, opts, &st); err != nil {
		return st, err
	}
	return st, nil
}

func syncDataset(db *gorm.DB, scanned ScannedDataset, opts SyncOptions, st *SyncStats) (store.Dataset, error) {
	meta := scanned.Meta
	want := store.Dataset{
		Slug:        meta.Slug,
		Dir:         scanned.Dir,
		Title:       meta.Title,
		Description: meta.Description,
		Emoji:       meta.Emoji,
		Color:       meta.Color,
	}

	var row store.Dataset
	err := db.Where("dir = ?", scanned.Dir).Take(&row).Error
	if store.IsNotFound(err) {
		// A legacy row has no dir yet and was matched by slug. Adopt it so its
		// articles — and their annotations — keep their dataset id.
		err = db.Where("slug = ?", meta.Slug).Take(&row).Error
	}
	switch {
	case store.IsNotFound(err):
		st.DatasetsCreated++
		log.Printf("[sync] + dataset %q (%s)", meta.Title, scanned.Dir)
		if opts.DryRun {
			return want, nil
		}
		if err := db.Create(&want).Error; err != nil {
			return store.Dataset{}, err
		}
		return want, nil
	case err != nil:
		return store.Dataset{}, err
	}

	if row.Slug == want.Slug && row.Dir == want.Dir && row.Title == want.Title &&
		row.Description == want.Description && row.Emoji == want.Emoji && row.Color == want.Color {
		return row, nil
	}
	st.DatasetsUpdated++
	log.Printf("[sync] ~ dataset %q (%s)", meta.Title, scanned.Dir)
	if opts.DryRun {
		return row, nil
	}
	row.Slug, row.Dir, row.Title = want.Slug, want.Dir, want.Title
	row.Description, row.Emoji, row.Color = want.Description, want.Emoji, want.Color
	if err := db.Save(&row).Error; err != nil {
		return store.Dataset{}, err
	}
	return row, nil
}

func syncArticle(db *gorm.DB, datasetID int64, art *Article, opts SyncOptions, st *SyncStats) (int64, error) {
	var row store.Article
	err := db.Where("rel_path = ?", art.RelPath).Take(&row).Error

	adopted := false
	if store.IsNotFound(err) {
		// The file may have been renamed or moved. Titles are unique within a
		// dataset, so a title match identifies the same article and keeps its id
		// — and therefore its annotations.
		err = db.Where("dataset_id = ? AND title = ? AND missing = ?", datasetID, art.Title, false).
			Take(&row).Error
		if err == nil {
			adopted = true
		}
	}

	switch {
	case store.IsNotFound(err):
		st.ArticlesCreated++
		log.Printf("[sync]   + %s", art.RelPath)
		if opts.DryRun {
			return 0, nil
		}
		created := store.Article{
			DatasetID:      datasetID,
			Title:          art.Title,
			Subtitle:       art.Subtitle,
			Level:          art.Level,
			Author:         art.Author,
			Origin:         art.Origin,
			PublishedAt:    art.PublishedAt,
			RelPath:        art.RelPath,
			ContentHash:    art.ContentHash,
			ParagraphCount: art.ParagraphCount,
			SentenceCount:  art.SentenceCount,
		}
		if err := db.Create(&created).Error; err != nil {
			return 0, err
		}
		return created.ID, nil

	case err != nil:
		return 0, err
	}

	hashChanged := row.ContentHash != art.ContentHash
	// A row with no hash yet is being *indexed for the first time* — that is
	// what happens to every article right after the migration off paragraph
	// rows, and it is not a content change. Only a hash that moves from one real
	// value to another means the text under existing annotations moved away, and
	// only that deserves the warning.
	contentChanged := hashChanged && row.ContentHash != ""
	metaChanged := row.DatasetID != datasetID || row.Title != art.Title ||
		row.Subtitle != art.Subtitle || row.Level != art.Level ||
		row.Author != art.Author || row.Origin != art.Origin ||
		row.RelPath != art.RelPath || row.ParagraphCount != art.ParagraphCount ||
		row.SentenceCount != art.SentenceCount || hashChanged ||
		!sameTime(row.PublishedAt, art.PublishedAt)

	if adopted {
		st.ArticlesAdopted++
		log.Printf("[sync]   > %s adopted (was matched by title; id %d kept)", art.RelPath, row.ID)
	}
	if !contentChanged && !metaChanged && !row.Missing {
		st.ArticlesKept++
		return row.ID, nil
	}

	st.ArticlesUpdated++
	if contentChanged {
		st.ArticlesChanged++
		log.Printf("[sync]   ! %s body changed (%s -> %s); annotations on edited "+
			"paragraphs no longer resolve, none were deleted",
			art.RelPath, shortHash(row.ContentHash), shortHash(art.ContentHash))
	}
	if opts.DryRun {
		return row.ID, nil
	}

	row.DatasetID = datasetID
	row.Title, row.Subtitle, row.Level = art.Title, art.Subtitle, art.Level
	row.Author, row.Origin = art.Author, art.Origin
	row.PublishedAt = art.PublishedAt
	row.RelPath = art.RelPath
	row.ContentHash = art.ContentHash
	row.ParagraphCount = art.ParagraphCount
	row.SentenceCount = art.SentenceCount
	row.Missing = false
	if err := db.Save(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

// markMissing flags index rows whose file is gone. They are hidden from every
// listing but keep their annotations, so restoring the file brings the article
// and its annotations back. Prune deletes them outright.
func markMissing(db *gorm.DB, seen map[int64]bool, opts SyncOptions, st *SyncStats) error {
	var rows []store.Article
	if err := db.Select("id", "rel_path", "missing").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if seen[row.ID] {
			continue
		}
		st.ArticlesMissing++
		if opts.Prune {
			st.ArticlesPruned++
			log.Printf("[sync]   - %s pruned (file is gone; its annotations go with it)", row.RelPath)
			if opts.DryRun {
				continue
			}
			if err := db.Delete(&store.Article{}, row.ID).Error; err != nil {
				return err
			}
			continue
		}
		if row.Missing {
			continue
		}
		log.Printf("[sync]   ? %s has no file any more — marked missing (use -prune to delete)", row.RelPath)
		if opts.DryRun {
			continue
		}
		if err := db.Model(&store.Article{}).Where("id = ?", row.ID).
			Update("missing", true).Error; err != nil {
			return err
		}
	}
	return nil
}

// sameTime compares two nullable timestamps by value.
func sameTime(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(*b)
	}
}

func shortHash(h string) string {
	if h == "" {
		return "(none)"
	}
	if len(h) <= 10 {
		return h
	}
	return h[:10]
}
