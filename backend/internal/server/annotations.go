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

	"english-reading/backend/internal/content"
	"english-reading/backend/internal/sentence"
	"english-reading/backend/internal/store"
)

// The three DTOs below carry paragraphHash instead of the old paragraphId.
// The hash is derived from the paragraph text, so an anchor keeps pointing at
// the same text when other paragraphs are added, removed or reordered.
type wordAnnotationDTO struct {
	ID            int64  `json:"id"`
	ArticleID     int64  `json:"articleId"`
	ParagraphHash string `json:"paragraphHash"`
	SentenceIndex int    `json:"sentenceIndex"`
	WordIndex     int    `json:"wordIndex"`
	Word          string `json:"word"`
	Pos           string `json:"pos"`
	Sense         string `json:"sense"`
	// Stale marks an anchor whose paragraph is no longer in the file. The row is
	// kept and reported; deleting a user's work is never automatic.
	Stale bool `json:"stale,omitempty"`
}

type noteDTO struct {
	ID            int64  `json:"id"`
	ArticleID     int64  `json:"articleId"`
	ParagraphHash string `json:"paragraphHash"`
	SentenceIndex int    `json:"sentenceIndex"`
	Content       string `json:"content"`
	Stale         bool   `json:"stale,omitempty"`
}

type translationDTO struct {
	ID             int64  `json:"id"`
	ArticleID      int64  `json:"articleId"`
	ParagraphHash  string `json:"paragraphHash"`
	SentenceIndex  int    `json:"sentenceIndex"`
	SourceText     string `json:"sourceText"`
	TranslatedText string `json:"translatedText"`
	Stale          bool   `json:"stale,omitempty"`
}

// The composite unique keys reused by the upserts below.
var (
	wordAnnotationKey  = []string{"user_id", "article_id", "paragraph_hash", "sentence_index", "word_index"}
	userTranslationKey = []string{"user_id", "article_id", "paragraph_hash", "sentence_index"}
)

func (s *Server) handleArticleState(w http.ResponseWriter, r *http.Request, user authUser) {
	articleID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的文章 ID")
		return
	}

	// The live hash set comes from the file, so every anchor can be labelled
	// fresh or stale in one pass instead of one file read per row.
	art, loaded, err := s.articleContent(articleID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	live := liveHashes(loaded)

	var annotations []store.WordAnnotation
	if err := s.db.Where("user_id = ? AND article_id = ?", user.ID, articleID).
		Order("sentence_index, word_index").
		Find(&annotations).Error; err != nil {
		log.Printf("load word annotations: %v", err)
		writeError(w, http.StatusInternalServerError, "读取单词标注失败")
		return
	}
	words := make([]wordAnnotationDTO, 0, len(annotations))
	for _, a := range annotations {
		words = append(words, wordAnnotationDTO{
			ID: a.ID, ArticleID: a.ArticleID, ParagraphHash: a.ParagraphHash,
			SentenceIndex: a.SentenceIndex, WordIndex: a.WordIndex,
			Word: a.Word, Pos: a.Pos,
			// Annotations saved before the escape handling landed stored
			// ECDICT's literal "\n"; clean it here too so the chip under the
			// word, the popup footer and the `selected` match against the
			// dictionary's now-normalised definition all agree.
			Sense: store.NormalizeDictText(a.Sense),
			Stale: !live[a.ParagraphHash],
		})
	}

	var noteRows []store.Note
	if err := s.db.Where("user_id = ? AND article_id = ?", user.ID, articleID).
		Order("sentence_index, id").
		Find(&noteRows).Error; err != nil {
		log.Printf("load notes: %v", err)
		writeError(w, http.StatusInternalServerError, "读取批注失败")
		return
	}
	notes := make([]noteDTO, 0, len(noteRows))
	for _, n := range noteRows {
		notes = append(notes, noteDTO{
			ID: n.ID, ArticleID: n.ArticleID, ParagraphHash: n.ParagraphHash,
			SentenceIndex: n.SentenceIndex, Content: n.Content,
			Stale: !live[n.ParagraphHash],
		})
	}

	var translationRows []store.UserTranslation
	if err := s.db.Where("user_id = ? AND article_id = ?", user.ID, articleID).
		Order("sentence_index").
		Find(&translationRows).Error; err != nil {
		log.Printf("load translations: %v", err)
		writeError(w, http.StatusInternalServerError, "读取翻译失败")
		return
	}
	translations := make([]translationDTO, 0, len(translationRows))
	for _, t := range translationRows {
		translations = append(translations, translationDTO{
			ID: t.ID, ArticleID: t.ArticleID, ParagraphHash: t.ParagraphHash,
			SentenceIndex: t.SentenceIndex, SourceText: t.SourceText,
			TranslatedText: t.TranslatedText,
			Stale:          !live[t.ParagraphHash],
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"wordAnnotations": words,
		"notes":           notes,
		"translations":    translations,
		"contentHash":     art.ContentHash,
		"liveContentHash": loaded.ContentHash,
	})
}

// ---------- word annotations ----------

type wordAnnotationRequest struct {
	ParagraphHash string `json:"paragraph_hash"`
	SentenceIndex int    `json:"sentence_index"`
	WordIndex     int    `json:"word_index"`
	Word          string `json:"word"`
	Pos           string `json:"pos"`
	Sense         string `json:"sense"`
	// Stale marks an anchor whose paragraph is no longer in the file. The row is
	// kept and reported; deleting a user's work is never automatic.
	Stale bool `json:"stale,omitempty"`
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
	if req.SentenceIndex < 0 || req.WordIndex < 0 {
		writeError(w, http.StatusBadRequest, "标注参数不完整")
		return
	}
	if req.Word == "" || req.Sense == "" {
		writeError(w, http.StatusBadRequest, "单词和释义不能为空")
		return
	}
	// Resolving the anchor against the file is what rejects an annotation aimed
	// at a paragraph that has since been edited away.
	text, err := s.sentenceText(articleID, req.ParagraphHash, req.SentenceIndex)
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
		UserID: user.ID, ArticleID: articleID, ParagraphHash: req.ParagraphHash,
		SentenceIndex: req.SentenceIndex, WordIndex: req.WordIndex,
		Word: req.Word, Pos: req.Pos, Sense: req.Sense,
	}
	// Upsert + read the id back in one transaction: GORM cannot return the
	// existing row id on MySQL, where clause.Returning is dropped and
	// LastInsertId() is 0 when the update is a no-op. See store.UpsertReturningID.
	id, err := store.UpsertReturningID(s.db, &row,
		upsertOnColumns(wordAnnotationKey, []string{"word", "pos", "sense", "updated_at"}),
		"user_id = ? AND article_id = ? AND paragraph_hash = ? AND sentence_index = ? AND word_index = ?",
		[]any{user.ID, articleID, req.ParagraphHash, req.SentenceIndex, req.WordIndex})
	if err != nil {
		log.Printf("upsert word annotation: %v", err)
		writeError(w, http.StatusInternalServerError, "保存单词标注失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "paragraphHash": req.ParagraphHash, "sentenceIndex": req.SentenceIndex,
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
	ParagraphHash string `json:"paragraph_hash"`
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
	if _, err := s.sentenceText(articleID, req.ParagraphHash, max(req.SentenceIndex, 0)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	note := store.Note{
		UserID: user.ID, ArticleID: articleID, ParagraphHash: req.ParagraphHash,
		SentenceIndex: req.SentenceIndex, Content: req.Content,
	}
	if err := s.db.Create(&note).Error; err != nil {
		log.Printf("insert note: %v", err)
		writeError(w, http.StatusInternalServerError, "保存批注失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": note.ID, "articleId": articleID, "paragraphHash": req.ParagraphHash,
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
	ParagraphHash string `json:"paragraph_hash"`
	SentenceIndex int    `json:"sentence_index"`
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
	text, err := s.sentenceText(articleID, req.ParagraphHash, req.SentenceIndex)
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
	err = s.db.Where("user_id = ? AND article_id = ? AND paragraph_hash = ? AND sentence_index = ?",
		user.ID, articleID, req.ParagraphHash, req.SentenceIndex).Take(&existing).Error
	if err == nil {
		writeJSON(w, http.StatusOK, translationDTO{
			ID: existing.ID, ArticleID: existing.ArticleID, ParagraphHash: existing.ParagraphHash,
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
		UserID: user.ID, ArticleID: articleID, ParagraphHash: req.ParagraphHash,
		SentenceIndex: req.SentenceIndex, SourceText: text, TranslatedText: translated,
	}
	savedID, err := store.UpsertReturningID(s.db, &row,
		upsertOnColumns(userTranslationKey, []string{"source_text", "translated_text", "updated_at"}),
		"user_id = ? AND article_id = ? AND paragraph_hash = ? AND sentence_index = ?",
		[]any{user.ID, articleID, req.ParagraphHash, req.SentenceIndex})
	if err != nil {
		log.Printf("insert translation: %v", err)
		writeError(w, http.StatusInternalServerError, "保存翻译失败")
		return
	}
	writeJSON(w, http.StatusOK, translationDTO{
		ID: savedID, ArticleID: articleID, ParagraphHash: req.ParagraphHash,
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

var (
	errParagraphNotFound = errors.New("段落不在当前正文中，原文可能已更新，请刷新后重试")
	errArticleFileGone   = errors.New("文章正文文件缺失或损坏")
)

// articleContent loads an article row plus its body file.
func (s *Server) articleContent(articleID int64) (*store.Article, *content.Article, error) {
	var art store.Article
	err := s.db.Where("id = ?", articleID).Take(&art).Error
	if store.IsNotFound(err) {
		return nil, nil, errors.New("文章不存在")
	}
	if err != nil {
		return nil, nil, err
	}
	loaded, err := s.content.LoadArticle(art.RelPath)
	if err != nil {
		log.Printf("load article content %s: %v", art.RelPath, err)
		return nil, nil, fmt.Errorf("%w：%s", errArticleFileGone, art.RelPath)
	}
	return &art, loaded, nil
}

// liveHashes is the set of paragraph hashes — plus the synthetic heading block
// — that currently exist in an article file.
func liveHashes(loaded *content.Article) map[string]bool {
	live := make(map[string]bool, loaded.ParagraphCount+1)
	for _, h := range loaded.ParagraphHashes {
		live[h] = true
	}
	live[content.ParagraphHash(loaded.Title)] = true
	return live
}

// sentenceText resolves the source text for either a whole paragraph
// (sentenceIndex < 0) or one sentence inside it.
//
// The paragraph is located by hash inside the article's file. An unknown hash
// means the paragraph was edited or removed since the client loaded the page,
// and the caller turns that into a 400 rather than storing an anchor that could
// never resolve again.
func (s *Server) sentenceText(articleID int64, paragraphHash string, sentenceIndex int) (string, error) {
	if strings.TrimSpace(paragraphHash) == "" {
		return "", errParagraphNotFound
	}
	_, loaded, err := s.articleContent(articleID)
	if err != nil {
		return "", err
	}
	// The heading block is synthesised by the reader and has no body, but a
	// whole-paragraph note on it is still meaningful.
	if sentenceIndex < 0 && paragraphHash == content.ParagraphHash(loaded.Title) {
		return loaded.Title, nil
	}
	text, ok := loaded.ParagraphByHash(paragraphHash)
	if !ok {
		return "", errParagraphNotFound
	}
	if sentenceIndex < 0 {
		return text, nil
	}
	sentences := sentence.Split(text)
	if sentenceIndex >= len(sentences) {
		return "", errors.New("句子序号已失效，请刷新后重试")
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
