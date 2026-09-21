package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"gorm.io/gorm/clause"

	"english-reading/backend/internal/sentence"
	"english-reading/backend/internal/store"
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

// The composite unique keys reused by the upserts below.
var (
	wordAnnotationKey  = []string{"user_id", "article_id", "paragraph_id", "sentence_index", "word_index"}
	userTranslationKey = []string{"user_id", "article_id", "paragraph_id", "sentence_index"}
)

func (s *Server) handleArticleState(w http.ResponseWriter, r *http.Request, user authUser) {
	articleID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文章 ID")
		return
	}

	var annotations []store.WordAnnotation
	if err := s.db.Where("user_id = ? AND article_id = ?", user.ID, articleID).
		Order("paragraph_id, sentence_index, word_index").
		Find(&annotations).Error; err != nil {
		log.Printf("load word annotations: %v", err)
		writeError(w, http.StatusInternalServerError, "读取单词标注失败")
		return
	}
	words := make([]wordAnnotationDTO, 0, len(annotations))
	for _, a := range annotations {
		words = append(words, wordAnnotationDTO{
			ID: a.ID, ArticleID: a.ArticleID, ParagraphID: a.ParagraphID,
			SentenceIndex: a.SentenceIndex, WordIndex: a.WordIndex,
			Word: a.Word, Pos: a.Pos,
			// Annotations saved before the escape handling landed stored
			// ECDICT's literal "\n"; clean it here too so the chip under the
			// word, the popup footer and the `selected` match against the
			// dictionary's now-normalised definition all agree.
			Sense: store.NormalizeDictText(a.Sense),
		})
	}

	var noteRows []store.Note
	if err := s.db.Where("user_id = ? AND article_id = ?", user.ID, articleID).
		Order("paragraph_id, sentence_index, id").
		Find(&noteRows).Error; err != nil {
		log.Printf("load notes: %v", err)
		writeError(w, http.StatusInternalServerError, "读取批注失败")
		return
	}
	notes := make([]noteDTO, 0, len(noteRows))
	for _, n := range noteRows {
		notes = append(notes, noteDTO{
			ID: n.ID, ArticleID: n.ArticleID, ParagraphID: n.ParagraphID,
			SentenceIndex: n.SentenceIndex, Content: n.Content,
		})
	}

	var translationRows []store.UserTranslation
	if err := s.db.Where("user_id = ? AND article_id = ?", user.ID, articleID).
		Order("paragraph_id, sentence_index").
		Find(&translationRows).Error; err != nil {
		log.Printf("load translations: %v", err)
		writeError(w, http.StatusInternalServerError, "读取翻译失败")
		return
	}
	translations := make([]translationDTO, 0, len(translationRows))
	for _, t := range translationRows {
		translations = append(translations, translationDTO{
			ID: t.ID, ArticleID: t.ArticleID, ParagraphID: t.ParagraphID,
			SentenceIndex: t.SentenceIndex, SourceText: t.SourceText,
			TranslatedText: t.TranslatedText,
		})
	}

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
	// A sense is dictionary text the reader picked, so it may still carry
	// ECDICT's literal "\n" when a client holds pre-fix data. Normalise before
	// trimming so a trailing escape becomes trimmable whitespace.
	req.Sense = strings.TrimSpace(store.NormalizeDictText(req.Sense))
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

	row := store.WordAnnotation{
		UserID: user.ID, ArticleID: articleID, ParagraphID: req.ParagraphID,
		SentenceIndex: req.SentenceIndex, WordIndex: req.WordIndex,
		Word: req.Word, Pos: req.Pos, Sense: req.Sense,
	}
	// Upsert + read the id back in one transaction: GORM cannot return the
	// existing row id on MySQL, where clause.Returning is dropped and
	// LastInsertId() is 0 when the update is a no-op. See store.UpsertReturningID.
	id, err := store.UpsertReturningID(s.db, &row,
		upsertOnColumns(wordAnnotationKey, []string{"word", "pos", "sense", "updated_at"}),
		"user_id = ? AND article_id = ? AND paragraph_id = ? AND sentence_index = ? AND word_index = ?",
		[]any{user.ID, articleID, req.ParagraphID, req.SentenceIndex, req.WordIndex})
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
	res := s.db.Where("id = ? AND user_id = ? AND article_id = ?", annotationID, user.ID, articleID).
		Delete(&store.WordAnnotation{})
	if res.Error != nil {
		log.Printf("delete word annotation: %v", res.Error)
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	if res.RowsAffected == 0 {
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

	note := store.Note{
		UserID: user.ID, ArticleID: articleID, ParagraphID: req.ParagraphID,
		SentenceIndex: req.SentenceIndex, Content: req.Content,
	}
	if err := s.db.Create(&note).Error; err != nil {
		log.Printf("insert note: %v", err)
		writeError(w, http.StatusInternalServerError, "保存批注失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": note.ID, "articleId": articleID, "paragraphId": req.ParagraphID,
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
	res := s.db.Where("id = ? AND user_id = ? AND article_id = ?", noteID, user.ID, articleID).
		Delete(&store.Note{})
	if res.Error != nil {
		log.Printf("delete note: %v", res.Error)
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	if res.RowsAffected == 0 {
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
	var existing store.UserTranslation
	err = s.db.Where("user_id = ? AND article_id = ? AND paragraph_id = ? AND sentence_index = ?",
		user.ID, articleID, req.ParagraphID, req.SentenceIndex).Take(&existing).Error
	if err == nil {
		writeJSON(w, http.StatusOK, translationDTO{
			ID: existing.ID, ArticleID: existing.ArticleID, ParagraphID: existing.ParagraphID,
			SentenceIndex: existing.SentenceIndex, SourceText: existing.SourceText,
			TranslatedText: existing.TranslatedText,
		})
		return
	}
	if !store.IsNotFound(err) {
		log.Printf("load translation: %v", err)
		writeError(w, http.StatusInternalServerError, "读取翻译记录失败")
		return
	}

	// Global cache by source text hash.
	translated, err := s.cachedTranslate(text)
	if err != nil {
		writeError(w, http.StatusBadGateway, "翻译服务暂时不可用："+err.Error())
		return
	}

	row := store.UserTranslation{
		UserID: user.ID, ArticleID: articleID, ParagraphID: req.ParagraphID,
		SentenceIndex: req.SentenceIndex, SourceText: text, TranslatedText: translated,
	}
	savedID, err := store.UpsertReturningID(s.db, &row,
		upsertOnColumns(userTranslationKey, []string{"source_text", "translated_text", "updated_at"}),
		"user_id = ? AND article_id = ? AND paragraph_id = ? AND sentence_index = ?",
		[]any{user.ID, articleID, req.ParagraphID, req.SentenceIndex})
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

	var cached store.TranslationCache
	err := s.db.Where("source_hash = ?", hash).Take(&cached).Error
	if err == nil {
		return cached.TranslatedText, nil
	}
	if !store.IsNotFound(err) {
		return "", err
	}

	translated, err := s.translate.Translate(text, "en", "zh-CN")
	if err != nil {
		return "", err
	}
	// Another request may have cached the same text meanwhile; ignore the
	// collision, exactly like the previous INSERT OR IGNORE.
	if err := s.db.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&store.TranslationCache{
			SourceHash: hash, SourceText: text,
			TranslatedText: translated, Target: "zh-CN",
		}).Error; err != nil {
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
	res := s.db.Where("id = ? AND user_id = ? AND article_id = ?", translationID, user.ID, articleID).
		Delete(&store.UserTranslation{})
	if res.Error != nil {
		log.Printf("delete translation: %v", res.Error)
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	if res.RowsAffected == 0 {
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
	var p store.Paragraph
	err := s.db.Where("id = ? AND article_id = ?", paragraphID, articleID).Take(&p).Error
	if store.IsNotFound(err) {
		return "", errParagraphNotFound
	}
	if err != nil {
		return "", err
	}
	if sentenceIndex < 0 {
		return p.Content, nil
	}
	sentences := sentence.Split(p.Content)
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
