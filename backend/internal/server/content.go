package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"english-reading/backend/internal/sentence"
	"english-reading/backend/internal/store"
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
	var rows []datasetDTO
	err := s.db.Model(&store.Dataset{}).
		Select(`datasets.id, datasets.slug, datasets.title, datasets.description,
		        datasets.emoji, datasets.color,
		        (SELECT COUNT(*) FROM articles a WHERE a.dataset_id = datasets.id) AS article_count`).
		Order("datasets.id").
		Scan(&rows).Error
	if err != nil {
		log.Printf("list datasets: %v", err)
		writeError(w, http.StatusInternalServerError, "读取数据集失败")
		return
	}
	if rows == nil {
		rows = []datasetDTO{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"datasets": rows})
}

// datasetArticlesRow is the flattened projection for the article list.
type datasetArticlesRow struct {
	ID             int64  `gorm:"column:id"`
	DatasetID      int64  `gorm:"column:dataset_id"`
	Title          string `gorm:"column:title"`
	Subtitle       string `gorm:"column:subtitle"`
	Level          string `gorm:"column:level"`
	ParagraphCount int64  `gorm:"column:paragraph_count"`
}

func (s *Server) handleDatasetArticles(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的数据集 ID")
		return
	}

	var ds store.Dataset
	err = s.db.Where("id = ?", id).Take(&ds).Error
	if store.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "数据集不存在")
		return
	}
	if err != nil {
		log.Printf("load dataset: %v", err)
		writeError(w, http.StatusInternalServerError, "读取数据集失败")
		return
	}

	var rows []datasetArticlesRow
	err = s.db.Model(&store.Article{}).
		Select(`articles.id, articles.dataset_id, articles.title, articles.subtitle, articles.level,
		        (SELECT COUNT(*) FROM paragraphs p WHERE p.article_id = articles.id AND p.kind = 'text') AS paragraph_count`).
		Where("articles.dataset_id = ?", id).
		Order("articles.id").
		Scan(&rows).Error
	if err != nil {
		log.Printf("list articles: %v", err)
		writeError(w, http.StatusInternalServerError, "读取文章列表失败")
		return
	}

	articles := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		articles = append(articles, map[string]any{
			"id": row.ID, "datasetId": row.DatasetID, "title": row.Title,
			"subtitle": row.Subtitle, "level": row.Level,
			"paragraphCount": row.ParagraphCount,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dataset": datasetDTO{
			ID: ds.ID, Slug: ds.Slug, Title: ds.Title, Description: ds.Description,
			Emoji: ds.Emoji, Color: ds.Color,
		},
		"articles": articles,
	})
}

func (s *Server) handleArticleDetail(w http.ResponseWriter, r *http.Request) {
	articleID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文章 ID")
		return
	}

	var art store.Article
	err = s.db.Where("id = ?", articleID).Take(&art).Error
	if store.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "文章不存在")
		return
	}
	if err != nil {
		log.Printf("load article: %v", err)
		writeError(w, http.StatusInternalServerError, "读取文章失败")
		return
	}

	var ds store.Dataset
	if err := s.db.Where("id = ?", art.DatasetID).Take(&ds).Error; err != nil {
		log.Printf("load dataset %d: %v", art.DatasetID, err)
		writeError(w, http.StatusInternalServerError, "读取数据集失败")
		return
	}

	var paras []store.Paragraph
	if err := s.db.Where("article_id = ?", articleID).
		Order("seq").Find(&paras).Error; err != nil {
		log.Printf("load paragraphs: %v", err)
		writeError(w, http.StatusInternalServerError, "读取段落失败")
		return
	}

	paragraphs := make([]paragraphDTO, 0, len(paras))
	for _, p := range paras {
		dto := paragraphDTO{
			ID: p.ID, Seq: p.Seq, Kind: p.Kind, Content: p.Content,
			Sentences: make([]sentenceDTO, 0),
		}
		if p.Kind == "text" {
			for i, text := range sentence.Split(p.Content) {
				dto.Sentences = append(dto.Sentences, sentenceDTO{Index: i, Text: text})
			}
		}
		paragraphs = append(paragraphs, dto)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"article": articleDTO{
			ID: art.ID, DatasetID: art.DatasetID, Title: art.Title,
			Subtitle: art.Subtitle, Level: art.Level,
		},
		"dataset": datasetDTO{
			ID: ds.ID, Slug: ds.Slug, Title: ds.Title, Description: ds.Description,
			Emoji: ds.Emoji, Color: ds.Color,
		},
		"paragraphs":        paragraphs,
		"prevArticleId":     neighbourArticleID(s.db, art.DatasetID, articleID, true),
		"nextArticleId":     neighbourArticleID(s.db, art.DatasetID, articleID, false),
		"paragraphCount":    len(paragraphs),
		"sentenceSplitInfo": "句子由后端按英文标点规则切分，标注键为 paragraph_id + sentence_index + word_index",
	})
}

// neighbourArticleID returns the id of the previous (before=true) or next
// article within the same dataset, or nil when there is none.
func neighbourArticleID(db *gorm.DB, datasetID, articleID int64, before bool) *int64 {
	query := db.Model(&store.Article{}).Where("dataset_id = ?", datasetID)
	if before {
		query = query.Where("id < ?", articleID).Order("id DESC")
	} else {
		query = query.Where("id > ?", articleID).Order("id ASC")
	}

	var found []int64
	if err := query.Limit(1).Pluck("id", &found).Error; err != nil || len(found) == 0 {
		return nil
	}
	id := found[0]
	return &id
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
		entry store.Dictionary
		found bool
	)
	for _, candidate := range candidates {
		// The column collation makes this match case-insensitively on both
		// engines (COLLATE NOCASE on SQLite, utf8mb4_general_ci on MySQL).
		var hit store.Dictionary
		err := s.db.Where("word = ?", candidate).Take(&hit).Error
		if store.IsNotFound(err) {
			continue
		}
		if err != nil {
			log.Printf("dict lookup %q: %v", word, err)
			writeError(w, http.StatusInternalServerError, "词典查询失败")
			return
		}
		entry, found = hit, true
		break
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"word": word, "found": false, "senses": []any{}})
		return
	}

	var senses []dictSenseDTO
	if err := json.Unmarshal([]byte(entry.SensesJSON), &senses); err != nil {
		writeError(w, http.StatusInternalServerError, "词典数据损坏")
		return
	}
	// Rows imported before the seeder learned to convert ECDICT's literal "\n"
	// still carry the escape; normalise on read so existing databases (local
	// SQLite and the deployed MySQL one) need no re-seed. See
	// store.NormalizeDictText. Idempotent, so already-clean rows are untouched.
	for i := range senses {
		senses[i].Def = store.NormalizeDictText(senses[i].Def)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"word": string(entry.Word), "phonetic": entry.Phonetic, "found": true, "senses": senses,
	})
}

type dictSenseDTO struct {
	Pos string `json:"pos"`
	Def string `json:"def"`
}
