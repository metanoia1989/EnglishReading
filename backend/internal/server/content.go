package server

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"english-reading/backend/internal/sentence"
)

func pathID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}

type datasetDTO struct {
	ID           int64  `json:"id"`
	Slug         string `json:"slug"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Emoji        string `json:"emoji"`
	Color        string `json:"color"`
	ArticleCount int64  `json:"articleCount"`
}

type articleDTO struct {
	ID        int64  `json:"id"`
	DatasetID int64  `json:"datasetId"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle"`
	Level     string `json:"level"`
}

type sentenceDTO struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

type paragraphDTO struct {
	ID        int64         `json:"id"`
	Seq       int           `json:"seq"`
	Kind      string        `json:"kind"`
	Content   string        `json:"content"`
	Sentences []sentenceDTO `json:"sentences"`
}

func (s *Server) handleDatasets(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`
		SELECT d.id, d.slug, d.title, d.description, d.emoji, d.color,
		       (SELECT COUNT(*) FROM articles a WHERE a.dataset_id = d.id) AS article_count
		FROM datasets d ORDER BY d.id`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取数据集失败")
		return
	}
	defer rows.Close()

	list := make([]datasetDTO, 0)
	for rows.Next() {
		var d datasetDTO
		if err := rows.Scan(&d.ID, &d.Slug, &d.Title, &d.Description, &d.Emoji, &d.Color, &d.ArticleCount); err != nil {
			writeError(w, http.StatusInternalServerError, "读取数据集失败")
			return
		}
		list = append(list, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"datasets": list})
}

func (s *Server) handleDatasetArticles(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的数据集 ID")
		return
	}
	var ds datasetDTO
	err = s.db.QueryRow(`SELECT id, slug, title, description, emoji, color FROM datasets WHERE id = ?`, id).
		Scan(&ds.ID, &ds.Slug, &ds.Title, &ds.Description, &ds.Emoji, &ds.Color)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "数据集不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取数据集失败")
		return
	}

	rows, err := s.db.Query(`
		SELECT a.id, a.dataset_id, a.title, a.subtitle, a.level,
		       (SELECT COUNT(*) FROM paragraphs p WHERE p.article_id = a.id AND p.kind = 'text') AS paragraph_count
		FROM articles a WHERE a.dataset_id = ? ORDER BY a.id`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取文章列表失败")
		return
	}
	defer rows.Close()

	articles := make([]map[string]any, 0)
	for rows.Next() {
		var a articleDTO
		var paragraphCount int64
		if err := rows.Scan(&a.ID, &a.DatasetID, &a.Title, &a.Subtitle, &a.Level, &paragraphCount); err != nil {
			writeError(w, http.StatusInternalServerError, "读取文章列表失败")
			return
		}
		articles = append(articles, map[string]any{
			"id": a.ID, "datasetId": a.DatasetID, "title": a.Title,
			"subtitle": a.Subtitle, "level": a.Level, "paragraphCount": paragraphCount,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"dataset": ds, "articles": articles})
}

func (s *Server) handleArticleDetail(w http.ResponseWriter, r *http.Request) {
	articleID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文章 ID")
		return
	}

	var art articleDTO
	var ds datasetDTO
	err = s.db.QueryRow(`
		SELECT a.id, a.dataset_id, a.title, a.subtitle, a.level,
		       d.id, d.slug, d.title, d.description, d.emoji, d.color
		FROM articles a JOIN datasets d ON d.id = a.dataset_id
		WHERE a.id = ?`, articleID).
		Scan(&art.ID, &art.DatasetID, &art.Title, &art.Subtitle, &art.Level,
			&ds.ID, &ds.Slug, &ds.Title, &ds.Description, &ds.Emoji, &ds.Color)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "文章不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取文章失败")
		return
	}

	rows, err := s.db.Query(`
		SELECT id, seq, kind, content FROM paragraphs WHERE article_id = ? ORDER BY seq`, articleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取段落失败")
		return
	}
	defer rows.Close()

	paragraphs := make([]paragraphDTO, 0)
	for rows.Next() {
		var p paragraphDTO
		if err := rows.Scan(&p.ID, &p.Seq, &p.Kind, &p.Content); err != nil {
			writeError(w, http.StatusInternalServerError, "读取段落失败")
			return
		}
		p.Sentences = make([]sentenceDTO, 0)
		if p.Kind == "text" {
			for i, text := range sentence.Split(p.Content) {
				p.Sentences = append(p.Sentences, sentenceDTO{Index: i, Text: text})
			}
		}
		paragraphs = append(paragraphs, p)
	}

	var prevID, nextID sql.NullInt64
	_ = s.db.QueryRow(`SELECT MAX(id) FROM articles WHERE dataset_id = ? AND id < ?`, art.DatasetID, articleID).Scan(&prevID)
	_ = s.db.QueryRow(`SELECT MIN(id) FROM articles WHERE dataset_id = ? AND id > ?`, art.DatasetID, articleID).Scan(&nextID)

	writeJSON(w, http.StatusOK, map[string]any{
		"article":          art,
		"dataset":          ds,
		"paragraphs":       paragraphs,
		"prevArticleId":    nullInt(prevID),
		"nextArticleId":    nullInt(nextID),
		"paragraphCount":   len(paragraphs),
		"sentenceSplitInfo": "句子由后端按英文标点规则切分，标注键为 paragraph_id + sentence_index + word_index",
	})
}

func nullInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func (s *Server) handleDictLookup(w http.ResponseWriter, r *http.Request) {
	word := r.URL.Query().Get("word")
	if word == "" {
		writeError(w, http.StatusBadRequest, "缺少 word 参数")
		return
	}

	candidates := []string{word}
	lower := strings.ToLower(word)
	contractionBases := map[string]string{
		"don't": "do", "doesn't": "does", "didn't": "do",
		"can't": "can", "cannot": "can", "won't": "will",
		"wouldn't": "would", "couldn't": "could", "shouldn't": "should",
		"isn't": "is", "aren't": "are", "wasn't": "was", "weren't": "were",
		"hasn't": "has", "haven't": "have", "hadn't": "had",
		"i'm": "i", "i've": "i", "i'd": "i", "i'll": "i",
		"you're": "you", "you've": "you", "you'd": "you", "you'll": "you",
		"he's": "he", "she's": "she", "it's": "it",
		"we're": "we", "we've": "we", "we'd": "we", "we'll": "we",
		"they're": "they", "they've": "they", "they'd": "they", "they'll": "they",
		"that's": "that", "there's": "there", "what's": "what",
	}
	if base, ok := contractionBases[lower]; ok {
		candidates = append(candidates, base)
	}
	if i := strings.IndexAny(word, "'’"); i > 0 {
		candidates = append(candidates, word[:i])
	}
	if i := strings.IndexAny(word, "-‐‑"); i > 0 {
		candidates = append(candidates, word[:i])
	}

	var (
		found     string
		phonetic  string
		sensesRaw string
	)
	for _, candidate := range candidates {
		err := s.db.QueryRow(`SELECT word, phonetic, senses_json FROM dictionary WHERE word = ? LIMIT 1`, candidate).
			Scan(&found, &phonetic, &sensesRaw)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			log.Printf("dict lookup %q: %v", word, err)
			writeError(w, http.StatusInternalServerError, "词典查询失败")
			return
		}
		break
	}
	if found == "" {
		writeJSON(w, http.StatusOK, map[string]any{"word": word, "found": false, "senses": []any{}})
		return
	}
	var senses []dictSenseDTO
	if err := json.Unmarshal([]byte(sensesRaw), &senses); err != nil {
		writeError(w, http.StatusInternalServerError, "词典数据损坏")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"word": found, "phonetic": phonetic, "found": true, "senses": senses,
	})
}

type dictSenseDTO struct {
	Pos string `json:"pos"`
	Def string `json:"def"`
}
