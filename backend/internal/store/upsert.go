package store

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpsertReturningID inserts a row, updates it when it collides with an
// existing unique key, and returns the row's primary key either way.
//
// Why this is not just `Clauses(clause.OnConflict{...}).Create(...)`:
//
// GORM back-fills the primary key differently per engine. On SQLite the
// dialector registers RETURNING, and GORM appends `RETURNING id` itself. MySQL
// has no RETURNING at all, so GORM silently drops `clause.Returning` and falls
// back to result.LastInsertId(). On MySQL that value is only correct when the
// statement actually changed the row:
//
//	insert                     → affected=1, LastInsertId=correct
//	collision, values changed   → affected=2, LastInsertId=correct
//	collision, values identical → affected=0, LastInsertId=0   ← the trap
//
// The "identical values" case is easy to hit here: the upsert also refreshes
// updated_at = CURRENT_TIMESTAMP, so re-saving the same annotation within the
// same second is a true no-op and would yield id 0 — which the frontend then
// uses for DELETE and fails.
//
// Reading the id back inside the same transaction (the unique key guarantees a
// single-row index lookup) behaves identically on both engines and keeps this
// migration free of dialect branches, which is the whole point of using an ORM.
func UpsertReturningID(
	db *gorm.DB,
	row any,
	conflict clause.OnConflict,
	where string,
	whereArgs []any,
) (int64, error) {
	var id int64
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(conflict).Create(row).Error; err != nil {
			return err
		}
		return tx.Model(row).Select("id").Where(where, whereArgs...).Scan(&id).Error
	})
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, errors.New("upsert: row missing after write")
	}
	return id, nil
}
