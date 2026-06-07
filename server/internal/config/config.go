// Package config loads YuSui Server configuration from the environment
// (12-factor). It fails fast on missing required values.
package config

import (
	"fmt"
	"os"
)

// Config holds all runtime configuration for the server.
type Config struct {
	// DatabaseURL is the Postgres DSN. For the `migrate` subcommand this must
	// be the yusui_migrate (DDL owner) role; for `serve` it must be yusui_app
	// (least-privilege runtime role). See deploy/postgres/init.
	DatabaseURL string
	HTTPAddr    string
	LogLevel    string
	Env         string
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	c := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		HTTPAddr:    getenv("HTTP_ADDR", ":8080"),
		LogLevel:    getenv("LOG_LEVEL", "info"),
		Env:         getenv("ENV", "dev"),
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("config: DATABASE_URL is required")
	}
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
