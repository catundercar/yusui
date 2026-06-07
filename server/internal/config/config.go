// Package config loads YuSui Server configuration from the environment
// (12-factor). It fails fast on missing required values.
package config

import (
	"fmt"
	"os"
	"time"
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

	// Auth (validated only by `serve`).
	JWTSecret    string
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
	StepUpWindow time.Duration

	// Admin bootstrap: on serve startup, if the users table is empty and
	// AdminPassword is set, seed an admin account (dev convenience).
	AdminUsername string
	AdminPassword string
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		LogLevel:      getenv("LOG_LEVEL", "info"),
		Env:           getenv("ENV", "dev"),
		JWTSecret:     os.Getenv("JWT_SECRET"),
		AccessTTL:     getdur("ACCESS_TTL", 15*time.Minute),
		RefreshTTL:    getdur("REFRESH_TTL", 7*24*time.Hour),
		StepUpWindow:  getdur("STEPUP_WINDOW", 30*time.Minute),
		AdminUsername: getenv("ADMIN_USERNAME", "admin"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("config: DATABASE_URL is required")
	}
	return c, nil
}

// RequireServe validates fields needed only by the serve command.
func (c Config) RequireServe() error {
	if c.JWTSecret == "" {
		return fmt.Errorf("config: JWT_SECRET is required for serve")
	}
	return nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getdur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
