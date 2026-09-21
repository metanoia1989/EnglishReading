package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Every test below runs against SQLite always, and against MySQL as well when
// TEST_MYSQL_DSN is set, e.g.
//
//	TEST_MYSQL_DSN='user:pass@tcp(host:3306)/db' go test ./internal/store/ -v
//
// This is what keeps the two engines honest: the upsert and the dictionary
// collation behave differently under the hood and are easy to get subtly wrong.
func forEachEngine(t *testing.T, fn func(t *testing.T, db *gorm.DB)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		db, err := Open(Config{Driver: DriverSQLite, DSN: filepath.Join(t.TempDir(), "test.db")})
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		fn(t, db)
	})
	if dsn := os.Getenv("TEST_MYSQL_DSN"); dsn != "" {
		t.Run("mysql", func(t *testing.T) { fn(t, mysqlHandle(t, dsn)) })
	} else {
		t.Log("TEST_MYSQL_DSN not set: skipping the MySQL half of this test")
	}
}

// The three tables GORM would pluralise wrongly must keep their real names.
func TestMigrationTableNames(t *testing.T) {
	forEachEngine(t, func(t *testing.T, db *gorm.DB) {
		want := []string{
			"users", "sessions", "pending_registrations", "datasets", "articles",
			"paragraphs", "dictionary", "word_annotations", "notes",
			"translation_cache", "meta", "user_translations",
		}
		for _, name := range want {
			if !db.Migrator().HasTable(name) {
				t.Errorf("table %q was not created", name)
			}
		}
		for _, bad := range []string{"dictionaries", "translation_caches", "metas"} {
			if db.Migrator().HasTable(bad) {
				t.Errorf("unexpected pluralised table %q exists", bad)
			}
		}
	})
}

// Foreign keys must exist so that re-seeding articles cascades.
func TestMigrationForeignKeysCascade(t *testing.T) {
	forEachEngine(t, func(t *testing.T, db *gorm.DB) {
		ds := Dataset{Slug: "aesop", Title: "伊索寓言"}
		if err := db.Create(&ds).Error; err != nil {
			t.Fatalf("create dataset: %v", err)
		}
		art := Article{DatasetID: ds.ID, Title: "The Fox", Level: "A1"}
		if err := db.Create(&art).Error; err != nil {
			t.Fatalf("create article: %v", err)
		}
		par := Paragraph{ArticleID: art.ID, Seq: 0, Kind: "text", Content: "hello"}
		if err := db.Create(&par).Error; err != nil {
			t.Fatalf("create paragraph: %v", err)
		}

		if err := db.Where("id = ?", art.ID).Delete(&Article{}).Error; err != nil {
			t.Fatalf("delete article: %v", err)
		}
		var count int64
		db.Model(&Paragraph{}).Where("article_id = ?", art.ID).Count(&count)
		if count != 0 {
			t.Errorf("paragraphs not cascaded: %d rows remain", count)
		}
	})
}

// SQLite needs COLLATE NOCASE for the case-insensitive headword lookup;
// MySQL relies on its utf8mb4_general_ci collation.
func TestDictionaryLookupIsCaseInsensitive(t *testing.T) {
	forEachEngine(t, func(t *testing.T, db *gorm.DB) {
		const word = "Paris"
		if err := db.Create(&Dictionary{
			Word: WordKey(word), Phonetic: "ˈpærɪs", SensesJSON: `[{"pos":"n.","def":"巴黎"}]`,
		}).Error; err != nil {
			t.Fatalf("seed dictionary: %v", err)
		}
		for _, probe := range []string{"paris", "PARIS", "Paris"} {
			var got Dictionary
			if err := db.Where("word = ?", probe).Take(&got).Error; err != nil {
				t.Errorf("lookup %q failed: %v", probe, err)
				continue
			}
			if string(got.Word) != word {
				t.Errorf("lookup %q returned %q, want original casing %q", probe, got.Word, word)
			}
		}
	})
}

// The no-op upsert trap: repeating an unchanged annotation must still return
// the original row id, not 0.
func TestUpsertReturningIDIsStableOnRepeat(t *testing.T) {
	forEachEngine(t, func(t *testing.T, db *gorm.DB) {
		u := User{Email: "u@e.x", Nickname: "u", PasswordHash: "h", CreatedAt: time.Now().UTC()}
		ds := Dataset{Slug: "s", Title: "t"}
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
		if err := db.Create(&ds).Error; err != nil {
			t.Fatalf("create dataset: %v", err)
		}
		art := Article{DatasetID: ds.ID, Title: "a"}
		if err := db.Create(&art).Error; err != nil {
			t.Fatalf("create article: %v", err)
		}
		par := Paragraph{ArticleID: art.ID, Seq: 0, Kind: "text", Content: "x"}
		if err := db.Create(&par).Error; err != nil {
			t.Fatalf("create paragraph: %v", err)
		}

		upsert := func(sense string) int64 {
			t.Helper()
			row := WordAnnotation{
				UserID: u.ID, ArticleID: art.ID, ParagraphID: par.ID,
				SentenceIndex: 0, WordIndex: 4, Word: "apple", Pos: "n.", Sense: sense,
			}
			id, err := UpsertReturningID(db, &row, clause.OnConflict{
				Columns: []clause.Column{
					{Name: "user_id"}, {Name: "article_id"}, {Name: "paragraph_id"},
					{Name: "sentence_index"}, {Name: "word_index"},
				},
				DoUpdates: clause.AssignmentColumns([]string{"word", "pos", "sense", "updated_at"}),
			}, "user_id = ? AND article_id = ? AND paragraph_id = ? AND sentence_index = ? AND word_index = ?",
				[]any{u.ID, art.ID, par.ID, 0, 4})
			if err != nil {
				t.Fatalf("upsert(%q): %v", sense, err)
			}
			return id
		}

		first := upsert("苹果")
		if first == 0 {
			t.Fatal("first upsert returned id 0")
		}
		// Identical values: on MySQL this is a true no-op (affected=0), which
		// is exactly where a naive OnConflict hands back 0.
		for i := 0; i < 3; i++ {
			if got := upsert("苹果"); got != first {
				t.Fatalf("repeat #%d returned id %d, want stable %d", i+1, got, first)
			}
		}
		if got := upsert("苹果树"); got != first {
			t.Fatalf("changed sense returned id %d, want stable %d", got, first)
		}

		var count int64
		db.Model(&WordAnnotation{}).Where("user_id = ?", u.ID).Count(&count)
		if count != 1 {
			t.Errorf("expected exactly 1 annotation row, got %d", count)
		}
	})
}

// Duplicate-key detection must not depend on engine-specific message text.
func TestIsDuplicate(t *testing.T) {
	forEachEngine(t, func(t *testing.T, db *gorm.DB) {
		const email = "dup@b.c"
		u := User{Email: email, Nickname: "a", PasswordHash: "h", CreatedAt: time.Now().UTC()}
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("first insert: %v", err)
		}
		dup := User{Email: email, Nickname: "b", PasswordHash: "h", CreatedAt: time.Now().UTC()}
		err := db.Create(&dup).Error
		if err == nil {
			t.Fatal("duplicate insert unexpectedly succeeded")
		}
		if !IsDuplicate(err) {
			t.Errorf("IsDuplicate(%v) = false, want true", err)
		}
	})
}

// Timestamps must round-trip as time.Time on both engines (the old code stored
// RFC3339 strings, which MySQL rejects outright with error 1292).
func TestTimeRoundTrip(t *testing.T) {
	forEachEngine(t, func(t *testing.T, db *gorm.DB) {
		want := time.Now().UTC().Truncate(time.Second)
		u := User{Email: "t@e.x", PasswordHash: "h", CreatedAt: want}
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
		s := Session{Token: "tok", UserID: u.ID, ExpiresAt: want}
		if err := db.Create(&s).Error; err != nil {
			t.Fatalf("create session: %v", err)
		}
		var got Session
		if err := db.Where("token = ?", s.Token).Take(&got).Error; err != nil {
			t.Fatalf("reload session: %v", err)
		}
		if !got.ExpiresAt.UTC().Equal(want) {
			t.Errorf("expires_at = %s, want %s", got.ExpiresAt.UTC(), want)
		}
		// A comparison against a bound time.Time must work (this is what
		// authenticate() and the session prune rely on).
		var n int64
		db.Model(&Session{}).
			Where("token = ? AND expires_at > ?", s.Token, want.Add(-time.Minute)).
			Count(&n)
		if n != 1 {
			t.Errorf("expires_at comparison matched %d rows, want 1", n)
		}
	})
}

// utf8mb4 must survive the round trip (Chinese titles, the 📚 emoji default).
func TestUnicodeRoundTrip(t *testing.T) {
	forEachEngine(t, func(t *testing.T, db *gorm.DB) {
		ds := Dataset{Slug: "uni", Title: "伊索寓言", Description: "狐狸与葡萄 🦊"}
		if err := db.Create(&ds).Error; err != nil {
			t.Fatalf("create dataset: %v", err)
		}
		var got Dataset
		if err := db.Where("id = ?", ds.ID).Take(&got).Error; err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got.Title != "伊索寓言" {
			t.Errorf("title = %q, want %q", got.Title, "伊索寓言")
		}
		if got.Description != "狐狸与葡萄 🦊" {
			t.Errorf("description = %q", got.Description)
		}
		if got.Emoji != "📚" {
			t.Errorf("emoji default = %q, want 📚", got.Emoji)
		}
	})
}

// Regression: GORM omits a zero-valued field from the INSERT when its tag
// carries a `default:`, letting the database default win. sentence_index = 0
// (a real sentence, not the -1 "whole paragraph" sentinel) was silently
// becoming -1, so sentence notes and translations landed on the wrong anchor
// and the upsert's read-back could not find the row at all.
func TestZeroValuedSentenceIndexIsPersisted(t *testing.T) {
	forEachEngine(t, func(t *testing.T, db *gorm.DB) {
		u := User{Email: "z@e.x", PasswordHash: "h", CreatedAt: time.Now().UTC()}
		ds := Dataset{Slug: "z", Title: "t"}
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
		if err := db.Create(&ds).Error; err != nil {
			t.Fatalf("create dataset: %v", err)
		}
		art := Article{DatasetID: ds.ID, Title: "a"}
		if err := db.Create(&art).Error; err != nil {
			t.Fatalf("create article: %v", err)
		}
		par := Paragraph{ArticleID: art.ID, Seq: 0, Kind: "text", Content: "x"}
		if err := db.Create(&par).Error; err != nil {
			t.Fatalf("create paragraph: %v", err)
		}

		note := Note{UserID: u.ID, ArticleID: art.ID, ParagraphID: par.ID,
			SentenceIndex: 0, Content: "sentence note"}
		if err := db.Create(&note).Error; err != nil {
			t.Fatalf("create note: %v", err)
		}
		var gotNote Note
		if err := db.Where("id = ?", note.ID).Take(&gotNote).Error; err != nil {
			t.Fatalf("reload note: %v", err)
		}
		if gotNote.SentenceIndex != 0 {
			t.Errorf("note sentence_index = %d, want 0", gotNote.SentenceIndex)
		}

		tr := UserTranslation{UserID: u.ID, ArticleID: art.ID, ParagraphID: par.ID,
			SentenceIndex: 0, SourceText: "x", TranslatedText: "y"}
		if err := db.Create(&tr).Error; err != nil {
			t.Fatalf("create translation: %v", err)
		}
		var gotTr UserTranslation
		if err := db.Where("id = ?", tr.ID).Take(&gotTr).Error; err != nil {
			t.Fatalf("reload translation: %v", err)
		}
		if gotTr.SentenceIndex != 0 {
			t.Errorf("translation sentence_index = %d, want 0", gotTr.SentenceIndex)
		}
	})
}
