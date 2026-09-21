package store

import (
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	mysqlOnce sync.Once
	mysqlDB   *gorm.DB
	mysqlErr  error
)

// mysqlHandle opens MySQL once per test binary and reuses the pool. Each fresh
// TCP connection costs ~10s because the server runs with skip_name_resolve=OFF
// and its resolver cannot answer reverse lookups for the LAN subnet, so paying
// that per test would dominate the run.
func mysqlHandle(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	mysqlOnce.Do(func() {
		bare, err := gorm.Open(dialectorForMust(dsn), silentConfig())
		if err != nil {
			mysqlErr = err
			return
		}
		dropAll(bare)
		if sqlDB, err := bare.DB(); err == nil {
			_ = sqlDB.Close()
		}
		mysqlDB, mysqlErr = Open(Config{Driver: DriverMySQL, DSN: dsn})
	})
	if mysqlErr != nil {
		t.Fatalf("open mysql: %v", mysqlErr)
	}
	purgeAll(mysqlDB)
	return mysqlDB
}

func dialectorForMust(dsn string) gorm.Dialector {
	d, err := dialectorFor(Config{Driver: DriverMySQL, DSN: dsn})
	if err != nil {
		panic(err)
	}
	return d
}

func silentConfig() *gorm.Config {
	return &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
}

// dropAll removes every model table in dependency order (children first).
func dropAll(db *gorm.DB) {
	models := AllModels()
	for i := len(models) - 1; i >= 0; i-- {
		_ = db.Migrator().DropTable(models[i])
	}
}

// purgeAll empties every table, keeping the schema in place.
func purgeAll(db *gorm.DB) {
	models := AllModels()
	for i := len(models) - 1; i >= 0; i-- {
		db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(models[i])
	}
}

func lowerWord(w string) string { return strings.ToLower(w) }
func upperWord(w string) string { return strings.ToUpper(w) }
