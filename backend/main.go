package main

import (
	"log"
	"net/http"
	"os"

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

	if err := seed.Run(db); err != nil {
		log.Fatalf("seed database: %v", err)
	}

	handler := server.New(db, distDir)
	addr := ":" + port
	log.Printf("[server] English Reading listening on http://127.0.0.1%s (db: %s)", addr, cfg.Driver)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
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
