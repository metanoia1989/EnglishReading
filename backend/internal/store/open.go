package store

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	gomysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open connects to the configured engine, applies the schema and returns a
// ready-to-use *gorm.DB.
func Open(cfg Config) (*gorm.DB, error) {
	dialector, err := dialectorFor(cfg)
	if err != nil {
		return nil, err
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		// Normalises driver errors into gorm.ErrDuplicatedKey /
		// gorm.ErrForeignKeyViolated across both engines.
		TranslateError: true,
		Logger: logger.New(log.New(os.Stderr, "", log.LstdFlags), logger.Config{
			SlowThreshold: 200 * time.Millisecond,
			LogLevel:      logLevel(),
			// A miss is normal here (probe rows, absent annotations); without
			// this every lookup would print "record not found".
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.Driver, err)
	}

	if err := tunePool(db, cfg.Driver); err != nil {
		return nil, err
	}
	if err := Migrate(db); err != nil {
		return nil, err
	}
	return db, nil
}

func dialectorFor(cfg Config) (gorm.Dialector, error) {
	switch cfg.Driver {
	case DriverMySQL:
		if strings.TrimSpace(cfg.DSN) == "" {
			return nil, fmt.Errorf("DB_DRIVER=mysql requires DB_DSN")
		}
		dsn, err := normalizeMySQLDSN(cfg.DSN)
		if err != nil {
			return nil, err
		}
		return gormmysql.Open(dsn), nil

	case DriverSQLite:
		dsn, err := sqliteDSN(cfg.DSN)
		if err != nil {
			return nil, err
		}
		return sqlite.Open(dsn), nil

	default:
		return nil, fmt.Errorf("unsupported DB_DRIVER %q (want %q or %q)",
			cfg.Driver, DriverSQLite, DriverMySQL)
	}
}

// sqliteDSN creates the parent directory and appends the pragmas the old
// hand-written store used to set explicitly.
func sqliteDSN(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty sqlite path")
	}
	// ":memory:" and other special names have no directory component.
	if dir := filepath.Dir(path); dir != "." && dir != "" && !strings.HasPrefix(path, ":") {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create data dir: %w", err)
		}
	}
	if strings.Contains(path, "?") {
		return path, nil
	}
	return path + "?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)", nil
}

// normalizeMySQLDSN forces the settings this app depends on, so a DSN copied
// from anywhere still behaves correctly:
//
//   - parseTime=true → DATETIME columns scan into time.Time. Without it the
//     driver returns "2006-01-02 15:04:05" strings and time parsing breaks.
//   - loc=UTC        → every timestamp is read back in UTC. The app stores UTC
//     everywhere; MySQL silently drops the offset from a literal, so a non-UTC
//     session would quietly shift values.
//   - charset=utf8mb4 → required for Chinese text and for the 📚 emoji in
//     datasets.emoji; 3-byte utf8 cannot store them.
//
// ReadTimeout is deliberately left untouched: the server runs with
// skip_name_resolve=OFF, so the initial handshake can take ~10s on a fresh
// TCP connection. A short read timeout would abort it.
func normalizeMySQLDSN(dsn string) (string, error) {
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("invalid DB_DSN: %w", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	if _, ok := cfg.Params["charset"]; !ok {
		cfg.Params["charset"] = "utf8mb4"
	}
	return cfg.FormatDSN(), nil
}

func tunePool(db *gorm.DB, driver string) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("unwrap sql.DB: %w", err)
	}
	switch driver {
	case DriverSQLite:
		// SQLite is single-writer: keep exactly one connection so the pragmas
		// above stay in effect and writes serialise instead of hitting
		// SQLITE_BUSY.
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(0)
	default:
		sqlDB.SetMaxOpenConns(25)
		sqlDB.SetMaxIdleConns(25)
		sqlDB.SetConnMaxLifetime(5 * time.Minute)
	}
	return nil
}

func logLevel() logger.LogLevel {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DB_LOG"))) {
	case "silent":
		return logger.Silent
	case "error":
		return logger.Error
	case "info":
		return logger.Info
	default:
		return logger.Warn
	}
}
