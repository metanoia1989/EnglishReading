package store

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ---------------------------------------------------------------------------
// Shared column types
// ---------------------------------------------------------------------------

// WordKey is a dictionary headword. Lookups must be case-insensitive:
// SQLite needs an explicit COLLATE NOCASE, while MySQL's default
// utf8mb4_general_ci collation is already case-insensitive.
type WordKey string

// GormDBDataType picks the column type per active engine.
func (WordKey) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Dialector.Name() == "sqlite" {
		return "TEXT COLLATE NOCASE"
	}
	return "VARCHAR(64)"
}

// ---------------------------------------------------------------------------
// Models — the single source of truth for the schema (GORM AutoMigrate)
// ---------------------------------------------------------------------------

// User is a registered account.
type User struct {
	ID           int64     `gorm:"primaryKey"`
	Email        string    `gorm:"size:191;not null;uniqueIndex"`
	Nickname     string    `gorm:"size:64;not null;default:''"`
	PasswordHash string    `gorm:"size:100;not null"`
	CreatedAt    time.Time `gorm:"not null"`
}

// Session is a bearer token issued at login.
type Session struct {
	Token     string    `gorm:"size:64;primaryKey"`
	UserID    int64     `gorm:"not null;index"`
	CreatedAt time.Time `gorm:"not null"`
	ExpiresAt time.Time `gorm:"not null;index"`
	User      *User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// PendingRegistration holds a registration awaiting email verification.
type PendingRegistration struct {
	Email        string    `gorm:"size:191;primaryKey"`
	PasswordHash string    `gorm:"size:100;not null"`
	Nickname     string    `gorm:"size:64;not null;default:''"`
	Code         string    `gorm:"size:16;not null"`
	ExpiresAt    time.Time `gorm:"not null"`
}

// Dataset groups articles into a themed collection. Its articles live on disk
// under <CONTENT_ROOT>/<Dir>; see internal/content.
type Dataset struct {
	ID   int64  `gorm:"primaryKey"`
	Slug string `gorm:"size:96;not null;uniqueIndex"`
	// default:'' keeps the one-time ADD COLUMN portable; see AGENTS §4.1 for why
	// a default whose value equals the zero value is safe.
	Dir         string `gorm:"size:191;not null;default:'';uniqueIndex"`
	Title       string `gorm:"size:191;not null"`
	Description string `gorm:"size:512;not null;default:''"`
	Emoji       string `gorm:"size:16;not null;default:'📚'"`
	Color       string `gorm:"size:32;not null;default:'#6366f1'"`
}

// Article is an index row for one JSON file under the content root. The body
// lives in the file; this row only carries what lists and lookups need, which
// is why it has no paragraph children any more.
//
// RelPath is the slash-separated path relative to <CONTENT_ROOT> and is UNIQUE.
// ContentHash is derived from the paragraph hashes of the file's contents, so
// any edit to the body changes it — that is how a stale annotation is detected
// instead of silently sliding onto the wrong word. ParagraphCount and
// SentenceCount are denormalised purely so the article list never has to open a
// file or run a COUNT subquery. There is no updated_at: the content hash and
// the sync report already say when a row changed.
//
// Every one of these columns carries an explicit `default:` because they are
// added to an existing table by the one-time migration, and SQLite refuses
// `ADD COLUMN ... NOT NULL` without one. Each default equals the field's zero
// value, which is the safe case described in §4.1 — the rule there exists to
// stop a default that *differs* from the zero value silently rewriting data,
// as SentenceIndex's old default:-1 did.
type Article struct {
	ID             int64      `gorm:"primaryKey"`
	DatasetID      int64      `gorm:"not null;index"`
	Title          string     `gorm:"size:512;not null"`
	Subtitle       string     `gorm:"size:512;not null;default:''"`
	Level          string     `gorm:"size:64;not null;default:''"`
	Author         string     `gorm:"size:191;not null;default:''"`
	Origin         string     `gorm:"size:512;not null;default:''"`
	PublishedAt    *time.Time `gorm:"index"`
	RelPath        string     `gorm:"size:512;not null;default:'';uniqueIndex"`
	ContentHash    string     `gorm:"size:64;not null;default:'';index"`
	ParagraphCount int        `gorm:"not null;default:0"`
	SentenceCount  int        `gorm:"not null;default:0"`
	// Missing marks an index row whose file disappeared from the content root.
	// Such rows are hidden from listings but keep their annotations, so putting
	// the file back restores the article and everything anchored to it.
	Missing   bool      `gorm:"not null;default:false;index"`
	CreatedAt time.Time `gorm:"not null"`
	Dataset   *Dataset  `gorm:"foreignKey:DatasetID;constraint:OnDelete:CASCADE"`
}

// Dictionary is the ECDICT-derived English→Chinese dictionary.
type Dictionary struct {
	Word       WordKey `gorm:"primaryKey"`
	Phonetic   string  `gorm:"size:191;not null;default:''"`
	SensesJSON string  `gorm:"type:text;not null"`
	Source     string  `gorm:"size:32;not null;default:'ECDICT'"`
}

// TableName keeps the singular table name (GORM would pluralise to
// "dictionaries").
func (Dictionary) TableName() string { return "dictionary" }

// WordAnnotation is a user's chosen part-of-speech + sense for one word
// occurrence. The composite unique key makes the upsert idempotent.
//
// The anchor is (article_id, paragraph_hash, sentence_index, word_index).
// ParagraphHash identifies the paragraph by its content rather than by a row id
// or a position, so inserting or reordering paragraphs elsewhere in the article
// leaves this annotation pointing at exactly the same text. Editing the
// paragraph itself changes its hash, which is how the app can report the
// annotation as stale instead of silently mis-pointing it.
type WordAnnotation struct {
	ID            int64     `gorm:"primaryKey"`
	UserID        int64     `gorm:"not null;uniqueIndex:uk_word_annotations,priority:1;index:idx_word_annotations_article,priority:1"`
	ArticleID     int64     `gorm:"not null;uniqueIndex:uk_word_annotations,priority:2;index:idx_word_annotations_article,priority:2"`
	ParagraphHash string    `gorm:"size:64;not null;default:'';uniqueIndex:uk_word_annotations,priority:3"`
	SentenceIndex int       `gorm:"not null;uniqueIndex:uk_word_annotations,priority:4"`
	WordIndex     int       `gorm:"not null;uniqueIndex:uk_word_annotations,priority:5"`
	Word          string    `gorm:"size:128;not null"`
	Pos           string    `gorm:"size:32;not null;default:''"`
	Sense         string    `gorm:"size:512;not null;default:''"`
	CreatedAt     time.Time `gorm:"not null"`
	UpdatedAt     time.Time `gorm:"not null"`
	User          *User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Article       *Article  `gorm:"foreignKey:ArticleID;constraint:OnDelete:CASCADE"`
}

// Note is a sentence note (sentence_index >= 0) or a whole-paragraph note
// (sentence_index = -1).
//
// NOTE: SentenceIndex deliberately carries no `default:` tag. GORM omits
// zero-valued fields from the INSERT when their tag has a default, so a
// `default:-1` here silently rewrote sentence_index 0 — a real sentence, the
// first one in every paragraph — into the -1 "whole paragraph" sentinel. Any
// field the application always sets explicitly must stay default-free; the
// zero value "" happens to equal the default for the remaining string columns,
// so those are safe.
type Note struct {
	ID            int64     `gorm:"primaryKey"`
	UserID        int64     `gorm:"not null;index:idx_notes_article,priority:1"`
	ArticleID     int64     `gorm:"not null;index:idx_notes_article,priority:2"`
	ParagraphHash string    `gorm:"size:64;not null;default:''"`
	SentenceIndex int       `gorm:"not null"`
	Content       string    `gorm:"type:text;not null"`
	CreatedAt     time.Time `gorm:"not null"`
	UpdatedAt     time.Time `gorm:"not null"`
	User          *User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Article       *Article  `gorm:"foreignKey:ArticleID;constraint:OnDelete:CASCADE"`
}

// TranslationCache is a global cache keyed by source-text hash, shared by all
// users to spare the free translation quota.
type TranslationCache struct {
	SourceHash     string    `gorm:"size:64;primaryKey"`
	SourceText     string    `gorm:"type:text;not null"`
	TranslatedText string    `gorm:"type:text;not null"`
	Target         string    `gorm:"size:16;not null;default:'zh-CN'"`
	CreatedAt      time.Time `gorm:"not null"`
}

// TableName keeps the singular table name (GORM would pluralise to
// "translation_caches").
func (TranslationCache) TableName() string { return "translation_cache" }

// UserTranslation is a user's own saved translation for a sentence or a whole
// paragraph.
type UserTranslation struct {
	ID             int64     `gorm:"primaryKey"`
	UserID         int64     `gorm:"not null;uniqueIndex:uk_user_translations,priority:1;index:idx_user_translations_article,priority:1"`
	ArticleID      int64     `gorm:"not null;uniqueIndex:uk_user_translations,priority:2;index:idx_user_translations_article,priority:2"`
	ParagraphHash  string    `gorm:"size:64;not null;default:'';uniqueIndex:uk_user_translations,priority:3"`
	SentenceIndex  int       `gorm:"not null;uniqueIndex:uk_user_translations,priority:4"`
	SourceText     string    `gorm:"type:text;not null"`
	TranslatedText string    `gorm:"type:text;not null"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
	User           *User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Article        *Article  `gorm:"foreignKey:ArticleID;constraint:OnDelete:CASCADE"`
}

// Meta stores small key/value bookkeeping rows (currently the seed versions).
type Meta struct {
	Key   string `gorm:"column:key;size:64;primaryKey"`
	Value string `gorm:"size:191;not null"`
}

// TableName keeps the singular table name (GORM would pluralise to "metas").
// Note the `key` column is a reserved word on MySQL; GORM quotes identifiers,
// so no escaping is needed at call sites.
func (Meta) TableName() string { return "meta" }

// AllModels is the AutoMigrate set, ordered so referenced tables come first.
//
// Paragraph is deliberately absent: article bodies live in JSON files under
// <CONTENT_ROOT>, and annotation anchors use content.paragraph_hash rather than
// a paragraph row id.
func AllModels() []any {
	return []any{
		&User{},
		&Session{},
		&PendingRegistration{},
		&Dataset{},
		&Article{},
		&Dictionary{},
		&WordAnnotation{},
		&Note{},
		&TranslationCache{},
		&Meta{},
		&UserTranslation{},
	}
}
