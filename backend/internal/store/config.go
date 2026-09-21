package store

import (
	"os"
	"strings"
)

// Driver names accepted by Config.Driver.
const (
	DriverSQLite = "sqlite"
	DriverMySQL  = "mysql"
)

// Config selects the database engine and its connection string.
type Config struct {
	Driver string
	DSN    string
}

// FromEnv builds a Config from the environment:
//
//	DB_DRIVER=sqlite (default)  →  DB_PATH  (default "data/app.db")
//	DB_DRIVER=mysql             →  DB_DSN   (required)
func FromEnv() Config {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DB_DRIVER"))) {
	case DriverMySQL:
		return Config{Driver: DriverMySQL, DSN: envOr("DB_DSN", "")}
	default:
		return Config{Driver: DriverSQLite, DSN: envOr("DB_PATH", "data/app.db")}
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
