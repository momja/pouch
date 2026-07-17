package server

import (
	"errors"
	"os"
	"strings"
)

// Config holds everything Pouch needs, sourced from environment variables.
type Config struct {
	Addr string

	WallabagURL          string
	WallabagClientID     string
	WallabagClientSecret string
	WallabagUsername     string
	WallabagPassword     string
}

// ConfigFromEnv reads configuration from the environment. The five
// WALLABAG_* variables are required.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		Addr:                 envOr("POUCH_ADDR", ":8080"),
		WallabagURL:          strings.TrimRight(os.Getenv("WALLABAG_URL"), "/"),
		WallabagClientID:     os.Getenv("WALLABAG_CLIENT_ID"),
		WallabagClientSecret: os.Getenv("WALLABAG_CLIENT_SECRET"),
		WallabagUsername:     os.Getenv("WALLABAG_USERNAME"),
		WallabagPassword:     os.Getenv("WALLABAG_PASSWORD"),
	}
	if cfg.WallabagURL == "" || cfg.WallabagClientID == "" || cfg.WallabagClientSecret == "" ||
		cfg.WallabagUsername == "" || cfg.WallabagPassword == "" {
		return cfg, errors.New("WALLABAG_URL, WALLABAG_CLIENT_ID, WALLABAG_CLIENT_SECRET, WALLABAG_USERNAME and WALLABAG_PASSWORD must all be set")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
