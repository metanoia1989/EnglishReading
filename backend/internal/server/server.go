package server

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"english-reading/backend/internal/translate"
)

// Server bundles the HTTP API dependencies.
type Server struct {
	db        *gorm.DB
	translate *translate.Client
}

// upsertOn builds an "insert, or update these columns on conflict" clause for
// the given conflict column. GORM renders it as ON CONFLICT ... DO UPDATE on
// SQLite and ON DUPLICATE KEY UPDATE on MySQL.
func upsertOn(conflictColumn string, updateColumns ...string) clause.OnConflict {
	return upsertOnColumns([]string{conflictColumn}, updateColumns)
}

// upsertOnColumns is upsertOn for composite unique keys.
func upsertOnColumns(conflictColumns, updateColumns []string) clause.OnConflict {
	columns := make([]clause.Column, 0, len(conflictColumns))
	for _, name := range conflictColumns {
		columns = append(columns, clause.Column{Name: name})
	}
	return clause.OnConflict{
		Columns:   columns,
		DoUpdates: clause.AssignmentColumns(updateColumns),
	}
}

// New builds the router, including CORS, API routes and (when a built
// frontend directory exists) the SPA static handler.
func New(db *gorm.DB, frontendDist string) http.Handler {
	s := &Server{db: db, translate: translate.New()}

	mux := http.NewServeMux()

	// Health.
	mux.HandleFunc("GET /api/health", s.handleHealth)

	// Auth.
	mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/auth/verify", s.handleVerifyRegister)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))

	// Content.
	mux.HandleFunc("GET /api/datasets", s.handleDatasets)
	mux.HandleFunc("GET /api/datasets/{id}/articles", s.handleDatasetArticles)
	mux.HandleFunc("GET /api/articles/{id}", s.handleArticleDetail)
	mux.HandleFunc("GET /api/articles/{id}/state", s.requireAuth(s.handleArticleState))

	// Dictionary.
	mux.HandleFunc("GET /api/dict/lookup", s.handleDictLookup)

	// Unknown API paths get a JSON 404 instead of the SPA shell.
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "接口不存在")
	})
	mux.HandleFunc("POST /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "接口不存在")
	})
	mux.HandleFunc("DELETE /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "接口不存在")
	})

	// Annotations / translations.
	mux.HandleFunc("POST /api/articles/{id}/word-annotations", s.requireAuth(s.handleUpsertWordAnnotation))
	mux.HandleFunc("DELETE /api/articles/{id}/word-annotations/{annotationId}", s.requireAuth(s.handleDeleteWordAnnotation))
	mux.HandleFunc("POST /api/articles/{id}/notes", s.requireAuth(s.handleCreateNote))
	mux.HandleFunc("DELETE /api/articles/{id}/notes/{noteId}", s.requireAuth(s.handleDeleteNote))
	mux.HandleFunc("POST /api/articles/{id}/translations", s.requireAuth(s.handleTranslate))
	mux.HandleFunc("DELETE /api/articles/{id}/translations/{translationId}", s.requireAuth(s.handleDeleteTranslation))

	distServed := false
	if dist, err := filepath.Abs(frontendDist); err == nil {
		if info, statErr := os.Stat(dist); statErr == nil && info.IsDir() {
			mux.Handle("GET /", spaHandler(dist))
			log.Printf("[server] serving frontend from %s", dist)
			distServed = true
		}
	}
	if !distServed {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{
				"app":    "English Reading API",
				"health": "/api/health",
				"hint":   "构建 frontend 后重启即可在 / 看到页面（npm run build）",
			})
		})
	}

	return logRequests(cors(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now().UTC()})
}

// ---------- helpers ----------

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

// spaHandler serves static files and falls back to index.html for client
// side routing.
func spaHandler(dist string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/api/") {
			p := filepath.Join(dist, filepath.Clean("/"+r.URL.Path))
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				http.ServeFile(w, r, p)
				return
			}
		}
		http.ServeFile(w, r, filepath.Join(dist, "index.html"))
	})
}

// requireAuth resolves the bearer token and passes the authenticated user to
// the wrapped handler.
func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request, authUser)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := s.authenticate(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		next(w, r, user)
	}
}

func (s *Server) authenticate(r *http.Request) (authUser, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return authUser{}, errors.New("missing token")
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if token == "" {
		return authUser{}, errors.New("empty token")
	}
	var u authUser
	err := s.db.Table("sessions").
		Select("users.id AS id, users.email AS email, users.nickname AS nickname").
		Joins("JOIN users ON users.id = sessions.user_id").
		Where("sessions.token = ? AND sessions.expires_at > ?", token, time.Now().UTC()).
		Take(&u).Error
	if err != nil {
		return authUser{}, err
	}
	return u, nil
}
