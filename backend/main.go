package main

import (
	"log"
	"net/http"
	"os"

	"gorm.io/gorm"

	"english-reading/backend/internal/content"
	"english-reading/backend/internal/seed"
	"english-reading/backend/internal/server"
	"english-reading/backend/internal/store"
)

func main() {
	port := envOr("PORT", "8080")
	distDir := envOr("FRONTEND_DIST", firstExisting("../frontend/dist", "frontend/dist"))

	cfg := store.FromEnv()
	db, err := store.Open(cfg)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	// Article bodies live on disk; the database is only their index.
	cs, err := content.Open("")
	if err != nil {
		log.Fatalf("open content root: %v", err)
	}
	if err := cs.EnsureRoot(); err != nil {
		log.Fatalf("prepare content root: %v", err)
	}
	log.Printf("[server] content root: %s", cs.Root())

	if err := seed.Run(db); err != nil {
		log.Fatalf("seed database: %v", err)
	}
	// On a fresh install the sample corpus is written into the tree so the
	// reader has something to show. An existing tree is never touched.
	if err := seed.MaterializeStarter(db, cs); err != nil {
		log.Fatalf("materialise starter content: %v", err)
	}

	// Index the tree at boot only when the index is not built yet — a first run,
	// or the first boot after the schema moved article bodies out of the
	// database. Re-indexing a large tree happens through cmd/articlesync, not on
	// every start.
	needs, err := needsBootSync(db)
	if err != nil {
		log.Fatalf("inspect article index: %v", err)
	}
	if needs {
		stats, err := content.Sync(db, cs, content.SyncOptions{})
		if err != nil {
			log.Fatalf("index content: %v", err)
		}
		log.Printf("[server] indexed content: %d dataset(s), %d article(s) created, %d adopted",
			stats.DatasetsCreated, stats.ArticlesCreated, stats.ArticlesAdopted)
	} else {
		log.Printf("[server] content index looks built; run `go run ./cmd/articlesync` after changing files")
	}

	handler := server.New(db, cs, distDir)
	addr := ":" + port
	log.Printf("[server] English Reading listening on http://127.0.0.1%s (db: %s)", addr, cfg.Driver)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}

// needsBootSync reports whether the article index has never been built.
//
// content_hash is empty only for rows that have not been through a sync — a
// freshly created row always has one, and pre-migration rows are given an empty
// value by AutoMigrate — so it doubles as an "index is stale" marker.
func needsBootSync(db *gorm.DB) (bool, error) {
	var datasets int64
	if err := db.Model(&store.Dataset{}).Count(&datasets).Error; err != nil {
		return false, err
	}
	if datasets == 0 {
		return true, nil
	}
	var unsynced int64
	if err := db.Model(&store.Article{}).Where("content_hash = ?", "").
		Count(&unsynced).Error; err != nil {
		return false, err
	}
	return unsynced > 0, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return p
		}
	}
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}
