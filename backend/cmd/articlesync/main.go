// Command articlesync keeps the database index in step with the content tree.
//
// The tree — <CONTENT_ROOT>/<dataset dir>/**/*.json — is the source of truth for
// article bodies. The database only indexes it, and this command is the only
// thing that writes that index.
//
// Article identity is preserved across runs: a row is matched by relative_path
// first and by (dataset, title) second, so renaming a file or fixing a title
// updates the existing row instead of creating a new one. Annotations hang off
// article_id, so that is what keeps them alive when the tree is reorganised.
//
// When a file's body changes, only the paragraphs that actually changed lose
// their anchors (their content hash changes). Sync reports that and deletes
// nothing: dropping a user's work is a decision for a human.
//
// Usage:
//
//	# see what the tree would do, without writing anything
//	go run ./cmd/articlesync -dry-run
//
//	# index the tree
//	go run ./cmd/articlesync
//
//	# write a cleaned corpus (tools/txt2articles.py output) into the tree, then index it
//	go run ./cmd/articlesync -materialize ../corpus.json -by-date
//
//	# additionally delete rows whose file has disappeared (cascades their annotations)
//	go run ./cmd/articlesync -prune
//
// It honours the same DB_DRIVER / DB_DSN / DB_PATH environment variables as the
// server, plus CONTENT_ROOT for the tree location.
package main

import (
	"flag"
	"fmt"
	"log"

	"english-reading/backend/internal/content"
	"english-reading/backend/internal/store"
)

func main() {
	log.SetFlags(0)

	var (
		root        = flag.String("content-root", "", "content root directory (default CONTENT_ROOT, else "+content.DefaultRoot+")")
		materialize = flag.String("materialize", "", "corpus JSON to write into the content tree before syncing")
		byDate      = flag.Bool("by-date", false, "with -materialize: nest articles under a YYYY-MM-DD directory")
		overwrite   = flag.Bool("overwrite", false, "with -materialize: rewrite article files that already exist")
		dryRun      = flag.Bool("dry-run", false, "report what would change, write nothing")
		prune       = flag.Bool("prune", false, "delete index rows whose file is gone (their annotations go too)")
		list        = flag.Bool("list", false, "print the scanned tree and exit without touching the database")
	)
	flag.Parse()

	cs, err := content.Open(*root)
	if err != nil {
		log.Fatalf("%v", err)
	}
	if err := cs.EnsureRoot(); err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("[sync] content root: %s", cs.Root())

	// A corpus can be materialised into the tree first; after that the files,
	// not the corpus document, are what gets read.
	if *materialize != "" {
		datasets, err := content.LoadCorpusFile(*materialize)
		if err != nil {
			log.Fatalf("read corpus: %v", err)
		}
		res, err := cs.MaterializeCorpus(datasets, content.MaterializeOptions{
			ByDate: *byDate, Overwrite: *overwrite, DryRun: *dryRun,
		})
		log.Printf("[sync] materialise: %d dataset(s), %d written, %d already present, %d overwritten",
			res.Datasets, res.Written, res.Skipped, res.Overwrite)
		if res.Skipped > 0 && !*overwrite {
			log.Printf("[sync] existing files were left untouched; pass -overwrite to replace them")
		}
	}

	if *list {
		datasets, bad := cs.Scan()
		for _, err := range bad {
			fmt.Printf("BAD  %v\n", err)
		}
		for _, ds := range datasets {
			fmt.Printf("%s  %s  (%d article(s))\n", ds.Dir, ds.Meta.Title, len(ds.Articles))
			for _, art := range ds.Articles {
				fmt.Printf("   %-52s %2d paragraphs %4d sentences  %s\n",
					art.RelPath, art.ParagraphCount, art.SentenceCount, art.ContentHash[:10])
			}
		}
		return
	}

	cfg := store.FromEnv()
	db, err := store.Open(cfg)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	stats, err := content.Sync(db, cs, content.SyncOptions{Prune: *prune, DryRun: *dryRun})
	if err != nil {
		log.Fatalf("sync: %v", err)
	}

	log.Printf("[sync] engine=%s", cfg.Driver)
	log.Printf("[sync] datasets: %d created, %d updated", stats.DatasetsCreated, stats.DatasetsUpdated)
	log.Printf("[sync] articles: %d created, %d updated, %d adopted by title, %d unchanged",
		stats.ArticlesCreated, stats.ArticlesUpdated, stats.ArticlesAdopted, stats.ArticlesKept)
	if stats.BadFiles > 0 {
		log.Printf("[sync] %d file(s) failed validation and were skipped (listed above); "+
			"they stay on disk untouched", stats.BadFiles)
	}
	if stats.ArticlesChanged > 0 {
		log.Printf("[sync] %d article(s) had edited bodies. Annotations on paragraphs that "+
			"changed no longer resolve; none were deleted. Open the article to see which.",
			stats.ArticlesChanged)
	}
	if stats.ArticlesMissing > 0 {
		if *prune {
			log.Printf("[sync] %d article(s) pruned because their file is gone", stats.ArticlesPruned)
		} else {
			log.Printf("[sync] %d article(s) have no file and were marked missing "+
				"(hidden from listings, annotations kept). Pass -prune to delete them.",
				stats.ArticlesMissing)
		}
	}
	if *dryRun {
		log.Printf("[sync] dry run: nothing was written")
	}
}
