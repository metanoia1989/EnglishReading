package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"english-reading/backend/internal/sentence"
)

type wordAnnotationDTO struct {
	ID            int64  `json:"id"`
	ArticleID     int64  `json:"articleId"`
	ParagraphID   int64  `json:"paragraphId"`
	SentenceIndex int    `json:"sentenceIndex"`
	WordIndex     int    `json:"wordIndex"`
	Word          string `json:"word"`
	Pos           string `json:"pos"`
	Sense         string `json:"sense"`
}

type noteDTO struct {
	ID            int64  `json:"id"`
	ArticleID     int64  `json:"articleId"`
	ParagraphID   int64  `json:"paragraphId"`
	SentenceIndex int    `json:"sentenceIndex"`
	Content       string `json:"content"`
}

type translationDTO struct {
	ID             int64  `json:"id"`
	ArticleID      int64  `json:"articleId"`
	ParagraphID    int64  `json:"paragraphId"`
	SentenceIndex  int    `json:"sentenceIndex"`
	SourceText     string `json:"sourceText"`
	TranslatedText string `json:"translatedText"`
}

func (s *Server) handleArticleState(w http.ResponseWriter, r *http.Request, user authUser) {
	articleID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文章 ID")
		return
	}

	words := make([]wordAnnotationDTO, 0)
	rows, err := s.db.Query(`
		SELECT id, article_id, paragraph_id, sentence_index, word_index, word, pos, sense
		FROM word_annotations WHERE user_id = ? AND article_id = ? ORDER BY paragraph_id, sentence_index, word_index`,
		user.ID, articleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取单词标注失败")
		return
	}
	for rows.Next() {
		var a wordAnnotationDTO
		if err := rows.Scan(&a.ID, &a.ArticleID, &a.ParagraphID, &a.SentenceIndex, &a.WordIndex, &a.Word, &a.Pos, &a.Sense); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "读取单词标注失败")
			return
		}
		words = append(words, a)
	}
	rows.Close()

	notes := make([]noteDTO, 0)
	rows, err = s.db.Query(`
		SELECT id, article_id, paragraph_id, sentence_index, content
		FROM notes WHERE user_id = ? AND article_id = ? ORDER BY paragraph_id, sentence_index, id`,
		user.ID, articleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取批注失败")
		return
	}
	for rows.Next() {
		var n noteDTO
		if err := rows.Scan(&n.ID, &n.ArticleID, &n.ParagraphID, &n.SentenceIndex, &n.Content); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "读取批注失败")
			return
		}
		notes = append(notes, n)
	}
	rows.Close()

	translations := make([]translationDTO, 0)
	rows, err = s.db.Query(`
		SELECT id, article_id, paragraph_id, sentence_index, source_text, translated_text
		FROM user_translations WHERE user_id = ? AND article_id = ? ORDER BY paragraph_id, sentence_index`,
		user.ID, articleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取翻译失败")
		return
	}
	for rows.Next() {
		var t translationDTO
		if err := rows.Scan(&t.ID, &t.ArticleID, &t.ParagraphID, &t.SentenceIndex, &t.SourceText, &t.TranslatedText); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "读取翻译失败")
			return
		}
		translations = append(translations, t)
	}
	rows.Close()

	writeJSON(w, http.StatusOK, map[string]any{
		"wordAnnotations": words,
		"notes":           notes,
		"translations":    translations,
	})
}

// ---------- word annotations ----------

type wordAnnotationRequest struct {
	ParagraphID   int64  `json:"paragraph_id"`
	SentenceIndex int    `json:"sentence_index"`
	WordIndex     int    `json:"word_index"`
	Word          string `json:"word"`
	Pos           string `json:"pos"`
	Sense         string `json:"sense"`
}

func (s *Server) handleUpsertWordAnnotation(w http.ResponseWriter, r *http.Request, user authUser) {
	articleID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文章 ID")
		return
	}
	var req wordAnnotationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	req.Word = strings.TrimSpace(req.Word)
	req.Pos = strings.TrimSpace(req.Pos)
	req.Sense = strings.TrimSpace(req.Sense)
	if req.ParagraphID <= 0 || req.SentenceIndex < 0 || req.WordIndex < 0 {
		writeError(w, http.StatusBadRequest, "标注参数不完整")
		return
	}
	if req.Word == "" || req.Sense == "" {
		writeError(w, http.StatusBadRequest, "单词和释义不能为空")
		return
	}
	text, err := s.sentenceText(articleID, req.ParagraphID, req.SentenceIndex)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The word index is an index into the frontend's word-token list for the
	// sentence. We only sanity-check that it is below the number of words.
	wordCount := countWordTokens(text)
	if req.WordIndex >= wordCount {
		writeError(w, http.StatusBadRequest, "单词位置已失效，请刷新后重试")
		return
	}

	var id int64
	err = s.db.QueryRow(`
		INSERT INTO word_annotations
			(user_id, article_id, paragraph_id, sentence_index, word_index, word, pos, sense)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, article_id, paragraph_id, sentence_index, word_index)
		DO UPDATE SET word = excluded.word, pos = excluded.pos, sense = excluded.sense,
		              updated_at = CURRENT_TIMESTAMP
		RETURNING id`,
		user.ID, articleID, req.ParagraphID, req.SentenceIndex, req.WordIndex,
		req.Word, req.Pos, req.Sense).Scan(&id)
	if err != nil {
		log.Printf("upsert word annotation: %v", err)
		writeError(w, http.StatusInternalServerError, "保存单词标注失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "paragraphId": req.ParagraphID, "sentenceIndex": req.SentenceIndex,
		"wordIndex": req.WordIndex, "word": req.Word, "pos": req.Pos, "sense": req.Sense,
	})
}

func (s *Server) handleDeleteWordAnnotation(w http.ResponseWriter, r *http.Request, user authUser) {
	articleID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文章 ID")
		return
	}
	annotationID, err := pathID(r, "annotationId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的标注 ID")
		return
	}
	res, err := s.db.Exec(`DELETE FROM word_annotations WHERE id = ? AND user_id = ? AND article_id = ?`,
		annotationID, user.ID, articleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "标注不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- notes ----------

type noteRequest struct {
	ParagraphID   int64  `json:"paragraph_id"`
	SentenceIndex int    `json:"sentence_index"`
	Content       string `json:"content"`
}

func (s *Server) handleCreateNote(w http.ResponseWriter, r *http.Request, user authUser) {
	articleID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文章 ID")
		return
	}
	var req noteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.SentenceIndex < -1 {
		req.SentenceIndex = -1
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "批注内容不能为空")
		return
	}
	if len([]rune(req.Content)) > 2000 {
		writeError(w, http.StatusBadRequest, "批注内容过长")
		return
	}
	if _, err := s.sentenceText(articleID, req.ParagraphID, max(req.SentenceIndex, 0)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	res, err := s.db.Exec(`
		INSERT INTO notes(user_id, article_id, paragraph_id, sentence_index, content)
		VALUES(?, ?, ?, ?, ?)`,
		user.ID, articleID, req.ParagraphID, req.SentenceIndex, req.Content)
	if err != nil {
		log.Printf("insert note: %v", err)
		writeError(w, http.StatusInternalServerError, "保存批注失败")
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "articleId": articleID, "paragraphId": req.ParagraphID,
		"sentenceIndex": req.SentenceIndex, "content": req.Content,
	})
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request, user authUser) {
	articleID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文章 ID")
		return
	}
	noteID, err := pathID(r, "noteId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的批注 ID")
		return
	}
	res, err := s.db.Exec(`DELETE FROM notes WHERE id = ? AND user_id = ? AND article_id = ?`, noteID, user.ID, articleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "批注不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- translations ----------

type translationRequest struct {
	ParagraphID   int64 `json:"paragraph_id"`
	SentenceIndex int   `json:"sentence_index"`
}

func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request, user authUser) {
	articleID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文章 ID")
		return
	}
	var req translationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if req.SentenceIndex < -1 {
		req.SentenceIndex = -1
	}
	text, err := s.sentenceText(articleID, req.ParagraphID, req.SentenceIndex)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(text) == "" {
		writeError(w, http.StatusBadRequest, "没有可翻译的内容")
		return
	}

	// Return the user's own saved translation when it exists.
	var existing translationDTO
	err = s.db.QueryRow(`
		SELECT id, article_id, paragraph_id, sentence_index, source_text, translated_text
		FROM user_translations
		WHERE user_id = ? AND article_id = ? AND paragraph_id = ? AND sentence_index = ?`,
		user.ID, articleID, req.ParagraphID, req.SentenceIndex).
		Scan(&existing.ID, &existing.ArticleID, &existing.ParagraphID, &existing.SentenceIndex,
			&existing.SourceText, &existing.TranslatedText)
	if err == nil {
		writeJSON(w, http.StatusOK, existing)
		return
	}
	if err != sql.ErrNoRows {
		writeError(w, http.StatusInternalServerError, "读取翻译记录失败")
		return
	}

	// Global cache by source text hash.
	translated, err := s.cachedTranslate(text)
	if err != nil {
		writeError(w, http.StatusBadGateway, "翻译服务暂时不可用："+err.Error())
		return
	}

	var savedID int64
	err = s.db.QueryRow(`
		INSERT INTO user_translations
			(user_id, article_id, paragraph_id, sentence_index, source_text, translated_text)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, article_id, paragraph_id, sentence_index)
		DO UPDATE SET source_text = excluded.source_text,
		              translated_text = excluded.translated_text,
		              updated_at = CURRENT_TIMESTAMP
		RETURNING id`,
		user.ID, articleID, req.ParagraphID, req.SentenceIndex, text, translated).Scan(&savedID)
	if err != nil {
		log.Printf("insert translation: %v", err)
		writeError(w, http.StatusInternalServerError, "保存翻译失败")
		return
	}
	writeJSON(w, http.StatusOK, translationDTO{
		ID: savedID, ArticleID: articleID, ParagraphID: req.ParagraphID,
		SentenceIndex: req.SentenceIndex, SourceText: text, TranslatedText: translated,
	})
}

func (s *Server) cachedTranslate(text string) (string, error) {
	hash := hashText(text)
	var cached string
	err := s.db.QueryRow(`SELECT translated_text FROM translation_cache WHERE source_hash = ?`, hash).Scan(&cached)
	if err == nil {
		return cached, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	translated, err := s.translate.Translate(text, "en", "zh-CN")
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO translation_cache(source_hash, source_text, translated_text, target)
		VALUES(?, ?, ?, 'zh-CN')`, hash, text, translated); err != nil {
		log.Printf("cache translation: %v", err)
	}
	return translated, nil
}

func (s *Server) handleDeleteTranslation(w http.ResponseWriter, r *http.Request, user authUser) {
	articleID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文章 ID")
		return
	}
	translationID, err := pathID(r, "translationId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的翻译 ID")
		return
	}
	res, err := s.db.Exec(`DELETE FROM user_translations WHERE id = ? AND user_id = ? AND article_id = ?`,
		translationID, user.ID, articleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "翻译记录不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- shared helpers ----------

var errParagraphNotFound = errors.New("段落不存在")

// sentenceText resolves the source text for either a paragraph
// (sentenceIndex == -1) or one sentence inside it.
func (s *Server) sentenceText(articleID, paragraphID int64, sentenceIndex int) (string, error) {
	var kind, content string
	err := s.db.QueryRow(`SELECT kind, content FROM paragraphs WHERE id = ? AND article_id = ?`,
		paragraphID, articleID).Scan(&kind, &content)
	if err == sql.ErrNoRows {
		return "", errParagraphNotFound
	}
	if err != nil {
		return "", err
	}
	if sentenceIndex < 0 {
		return content, nil
	}
	sentences := sentence.Split(content)
	if sentenceIndex >= len(sentences) {
		return "", fmt.Errorf("句子序号已失效")
	}
	return sentences[sentenceIndex], nil
}

func countWordTokens(text string) int {
	count := 0
	inWord := false
	for _, r := range text {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if isLetter && !inWord {
			count++
			inWord = true
		} else if !isLetter {
			inWord = false
		}
	}
	return count
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text) + "\x00zh-CN"))
	return hex.EncodeToString(sum[:])
}
