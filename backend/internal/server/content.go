package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"english-reading/backend/internal/content"
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
	Author    string `json:"author"`
	Origin    string `json:"origin"`
	// PublishedAt is RFC3339 or null; the reader shows it when present.
	PublishedAt *string `json:"publishedAt"`
	// ParagraphCount and SentenceCount are columns denormalised at sync time,
	// so listing articles never opens a file or runs a COUNT.
	ParagraphCount int  `json:"paragraphCount"`
	SentenceCount  int  `json:"sentenceCount"`
	ContentChanged bool `json:"contentChanged,omitempty"`
}

type sentenceDTO struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

// paragraphDTO is one rendered block of an article.
//
// Hash replaces the old paragraph row id and is the annotation anchor: it is
// derived from the paragraph text, so it survives inserting or reordering other
// paragraphs and changes exactly when this paragraph's text changes.
type paragraphDTO struct {
	Hash      string        `json:"hash"`
	Index     int           `json:"index"`
	Kind      string        `json:"kind"`
	Content   string        `json:"content"`
	Sentences []sentenceDTO `json:"sentences"`
}

// articleDetail is what GET /api/articles/{id} returns.
type articleDetail struct {
	Article      articleDTO     `json:"article"`
	Dataset      datasetDTO     `json:"dataset"`
	Paragraphs   []paragraphDTO `json:"paragraphs"`
	PrevArticle  *int64         `json:"prevArticleId"`
	NextArticle  *int64         `json:"nextArticleId"`
	ParagraphCnt int            `json:"paragraphCount"`
	// StaleAnchors counts this user's annotations whose paragraph hash is no
	// longer present in the file, i.e. the text under them was edited. They are
	// reported, never deleted.
	StaleAnchors int `json:"staleAnchors"`
}

func (s *Server) handleDatasets(w http.ResponseWriter, r *http.Request) {
	// The count runs once per dataset, and skips articles whose file is gone so
	// a deleted file stops being advertised.
	var rows []datasetDTO
	err := s.db.Model(&store.Dataset{}).
		Select(`datasets.id, datasets.slug, datasets.title, datasets.description,
		        datasets.emoji, datasets.color,
		        (SELECT COUNT(*) FROM articles a
		          WHERE a.dataset_id = datasets.id AND a.missing = ?) AS article_count`, false).
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
//
// There is no paragraph subquery any more: paragraph_count and sentence_count
// are columns maintained by content.Sync, which is what makes listing a dataset
// with tens of thousands of articles cheap.
type datasetArticlesRow struct {
	ID             int64   `gorm:"column:id"`
	DatasetID      int64   `gorm:"column:dataset_id"`
	Title          string  `gorm:"column:title"`
	Subtitle       string  `gorm:"column:subtitle"`
	Level          string  `gorm:"column:level"`
	Author         string  `gorm:"column:author"`
	Origin         string  `gorm:"column:origin"`
	PublishedAt    *string `gorm:"column:published_at"`
	ParagraphCount int     `gorm:"column:paragraph_count"`
	SentenceCount  int     `gorm:"column:sentence_count"`
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
		Select(`articles.id, articles.dataset_id, articles.title, articles.subtitle,
		        articles.level, articles.author, articles.origin,
		        articles.published_at, articles.paragraph_count, articles.sentence_count`).
		Where("articles.dataset_id = ? AND articles.missing = ?", id, false).
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
			"subtitle": row.Subtitle, "level": row.Level, "author": row.Author,
			"origin": row.Origin, "publishedAt": row.PublishedAt,
			"paragraphCount": row.ParagraphCount, "sentenceCount": row.SentenceCount,
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

	loaded, err := s.content.LoadArticle(art.RelPath)
	if err != nil {
		// A missing or unreadable file is a content problem, not a server bug:
		// say which file instead of returning an unexplained 500.
		log.Printf("load article content %s: %v", art.RelPath, err)
		writeError(w, http.StatusNotFound, "文章正文文件缺失或损坏："+art.RelPath)
		return
	}

	paragraphs := make([]paragraphDTO, 0, loaded.ParagraphCount+1)
	// The title is rendered as a heading block so the reader keeps a scroll
	// anchor for it, exactly like the old heading paragraph did.
	paragraphs = append(paragraphs, paragraphDTO{
		Hash: content.ParagraphHash(loaded.Title), Index: 0, Kind: "heading",
		Content: loaded.Title, Sentences: []sentenceDTO{},
	})
	for i, text := range loaded.Paragraphs {
		sentences := make([]sentenceDTO, 0, loaded.SentenceCounts[i])
		for j, stext := range sentence.Split(text) {
			sentences = append(sentences, sentenceDTO{Index: j, Text: stext})
		}
		paragraphs = append(paragraphs, paragraphDTO{
			Hash: loaded.ParagraphHashes[i], Index: i + 1, Kind: "text",
			Content: text, Sentences: sentences,
		})
	}

	writeJSON(w, http.StatusOK, articleDetail{
		Article: articleDTO{
			ID: art.ID, DatasetID: art.DatasetID, Title: loaded.Title,
			Subtitle: loaded.Subtitle, Level: loaded.Level, Author: loaded.Author,
			Origin: loaded.Origin, PublishedAt: formatTimePtr(loaded.PublishedAt),
			ParagraphCount: loaded.ParagraphCount, SentenceCount: loaded.SentenceCount,
			ContentChanged: art.ContentHash != loaded.ContentHash,
		},
		Dataset: datasetDTO{
			ID: ds.ID, Slug: ds.Slug, Title: ds.Title, Description: ds.Description,
			Emoji: ds.Emoji, Color: ds.Color,
		},
		Paragraphs:   paragraphs,
		PrevArticle:  neighbourArticleID(s.db, art.DatasetID, articleID, true),
		NextArticle:  neighbourArticleID(s.db, art.DatasetID, articleID, false),
		ParagraphCnt: len(paragraphs),
		StaleAnchors: s.staleAnchorCount(r, art.ID, loaded),
	})
}

// staleAnchorCount reports how many of the viewer's annotation anchors no
// longer resolve to a paragraph in the file. Informational only: a stale anchor
// means the text under it was edited, not that anything was lost.
func (s *Server) staleAnchorCount(r *http.Request, articleID int64, loaded *content.Article) int {
	user, err := s.authenticate(r)
	if err != nil {
		return 0
	}
	var hashes []string
	if err := s.db.Model(&store.WordAnnotation{}).
		Where("user_id = ? AND article_id = ?", user.ID, articleID).
		Distinct().Pluck("paragraph_hash", &hashes).Error; err != nil {
		return 0
	}
	stale := 0
	for _, h := range hashes {
		if _, ok := loaded.ParagraphByHash(h); !ok {
			stale++
		}
	}
	return stale
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// neighbourArticleID returns the id of the previous (before=true) or next
// article within the same dataset, or nil when there is none.
func neighbourArticleID(db *gorm.DB, datasetID, articleID int64, before bool) *int64 {
	query := db.Model(&store.Article{}).
		Where("dataset_id = ? AND missing = ?", datasetID, false)
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

// dictSenseDTO is one dictionary sense shown in the word popup.
type dictSenseDTO struct {
	Pos string `json:"pos"`
	Def string `json:"def"`
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
