package store

import (
	"errors"

	"gorm.io/gorm"
)

// IsDuplicate reports whether err is a unique-constraint violation on any
// supported engine.
//
// This replaces the old `strings.Contains(err.Error(), "UNIQUE")` check, which
// silently stopped working on MySQL: MySQL reports duplicate keys as
// "Error 1062: Duplicate entry ... for key ..." and never contains the word
// "UNIQUE". gorm.Config{TranslateError: true} normalises driver errors into
// gorm.ErrDuplicatedKey for both SQLite and MySQL.
func IsDuplicate(err error) bool {
	return err != nil && errors.Is(err, gorm.ErrDuplicatedKey)
}

// IsNotFound reports whether err is gorm.ErrRecordNotFound (the ORM
// equivalent of sql.ErrNoRows).
func IsNotFound(err error) bool {
	return err != nil && errors.Is(err, gorm.ErrRecordNotFound)
}
