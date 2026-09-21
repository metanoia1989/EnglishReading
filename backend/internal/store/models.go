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

// Dataset groups articles into a themed collection.
type Dataset struct {
	ID          int64  `gorm:"primaryKey"`
	Slug        string `gorm:"size:96;not null;uniqueIndex"`
	Title       string `gorm:"size:191;not null"`
	Description string `gorm:"size:512;not null;default:''"`
	Emoji       string `gorm:"size:16;not null;default:'📚'"`
	Color       string `gorm:"size:32;not null;default:'#6366f1'"`
}

// Article belongs to a Dataset.
type Article struct {
	ID        int64     `gorm:"primaryKey"`
	DatasetID int64     `gorm:"not null;index"`
	Title     string    `gorm:"size:512;not null"`
	Subtitle  string    `gorm:"size:512;not null;default:''"`
	Level     string    `gorm:"size:64;not null;default:''"`
	CreatedAt time.Time `gorm:"not null"`
	Dataset   *Dataset  `gorm:"foreignKey:DatasetID;constraint:OnDelete:CASCADE"`
}

// Paragraph is a heading or a body block. Body sentences are split at read
// time, so sentence anchors stay stable as paragraph_id + sentence_index.
type Paragraph struct {
	ID        int64    `gorm:"primaryKey"`
	ArticleID int64    `gorm:"not null;index:idx_paragraphs_article,priority:1"`
	Seq       int      `gorm:"not null;index:idx_paragraphs_article,priority:2"`
	Kind      string   `gorm:"size:16;not null;default:'text'"`
	Content   string   `gorm:"type:text;not null"`
	Article   *Article `gorm:"foreignKey:ArticleID;constraint:OnDelete:CASCADE"`
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
type WordAnnotation struct {
	ID            int64      `gorm:"primaryKey"`
	UserID        int64      `gorm:"not null;uniqueIndex:uk_word_annotations,priority:1;index:idx_word_annotations_article,priority:1"`
	ArticleID     int64      `gorm:"not null;uniqueIndex:uk_word_annotations,priority:2;index:idx_word_annotations_article,priority:2"`
	ParagraphID   int64      `gorm:"not null;uniqueIndex:uk_word_annotations,priority:3"`
	SentenceIndex int        `gorm:"not null;uniqueIndex:uk_word_annotations,priority:4"`
	WordIndex     int        `gorm:"not null;uniqueIndex:uk_word_annotations,priority:5"`
	Word          string     `gorm:"size:128;not null"`
	Pos           string     `gorm:"size:32;not null;default:''"`
	Sense         string     `gorm:"size:512;not null;default:''"`
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null"`
	User          *User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Article       *Article   `gorm:"foreignKey:ArticleID;constraint:OnDelete:CASCADE"`
	Paragraph     *Paragraph `gorm:"foreignKey:ParagraphID;constraint:OnDelete:CASCADE"`
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
	ID            int64      `gorm:"primaryKey"`
	UserID        int64      `gorm:"not null;index:idx_notes_article,priority:1"`
	ArticleID     int64      `gorm:"not null;index:idx_notes_article,priority:2"`
	ParagraphID   int64      `gorm:"not null"`
	SentenceIndex int        `gorm:"not null"`
	Content       string     `gorm:"type:text;not null"`
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null"`
	User          *User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Article       *Article   `gorm:"foreignKey:ArticleID;constraint:OnDelete:CASCADE"`
	Paragraph     *Paragraph `gorm:"foreignKey:ParagraphID;constraint:OnDelete:CASCADE"`
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
	ID             int64      `gorm:"primaryKey"`
	UserID         int64      `gorm:"not null;uniqueIndex:uk_user_translations,priority:1;index:idx_user_translations_article,priority:1"`
	ArticleID      int64      `gorm:"not null;uniqueIndex:uk_user_translations,priority:2;index:idx_user_translations_article,priority:2"`
	ParagraphID    int64      `gorm:"not null;uniqueIndex:uk_user_translations,priority:3"`
	SentenceIndex  int        `gorm:"not null;uniqueIndex:uk_user_translations,priority:4"`
	SourceText     string     `gorm:"type:text;not null"`
	TranslatedText string     `gorm:"type:text;not null"`
	CreatedAt      time.Time  `gorm:"not null"`
	UpdatedAt      time.Time  `gorm:"not null"`
	User           *User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Article        *Article   `gorm:"foreignKey:ArticleID;constraint:OnDelete:CASCADE"`
	Paragraph      *Paragraph `gorm:"foreignKey:ParagraphID;constraint:OnDelete:CASCADE"`
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
func AllModels() []any {
	return []any{
		&User{},
		&Session{},
		&PendingRegistration{},
		&Dataset{},
		&Article{},
		&Paragraph{},
		&Dictionary{},
		&WordAnnotation{},
		&Note{},
		&TranslationCache{},
		&Meta{},
		&UserTranslation{},
	}
}
