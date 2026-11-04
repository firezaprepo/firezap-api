// Package config loads the server configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every setting the server needs. All values come from the environment
// (see .env.example), so the same binary runs anywhere without rebuilding.
type Config struct {
	AdminKey        string
	DatabaseURL     string
	HTTPPort        int
	PublicURL       string
	WebhookTimeout  time.Duration
	DefaultLanguage string
	LogLevel        string
	// SendMinInterval is the minimum time between two messages sent by the same instance.
	// It keeps the sending pace close to normal human use, which protects the number.
	SendMinInterval time.Duration
}

// minAdminKeyLength guards against weak admin keys: the admin key controls every instance.
const minAdminKeyLength = 24

// Load reads the configuration from the environment and validates it.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	cfg := Config{
		AdminKey:        get("FZ_ADMIN_KEY", ""),
		DatabaseURL:     get("DATABASE_URL", ""),
		PublicURL:       strings.TrimRight(get("PUBLIC_URL", ""), "/"),
		DefaultLanguage: get("DEFAULT_LANGUAGE", "en"),
		LogLevel:        strings.ToLower(get("LOG_LEVEL", "info")),
	}

	var errs []error
	if cfg.AdminKey == "" {
		errs = append(errs, errors.New("FZ_ADMIN_KEY is required"))
	} else if len(cfg.AdminKey) < minAdminKeyLength {
		errs = append(errs, fmt.Errorf("FZ_ADMIN_KEY must have at least %d characters", minAdminKeyLength))
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}

	port, err := strconv.Atoi(get("HTTP_PORT", "8080"))
	if err != nil || port < 1 || port > 65535 {
		errs = append(errs, errors.New("HTTP_PORT must be a number between 1 and 65535"))
	}
	cfg.HTTPPort = port

	timeout, err := strconv.Atoi(get("WEBHOOK_TIMEOUT", "10"))
	if err != nil || timeout < 1 || timeout > 120 {
		errs = append(errs, errors.New("WEBHOOK_TIMEOUT must be between 1 and 120 seconds"))
	}
	cfg.WebhookTimeout = time.Duration(timeout) * time.Second

	interval, err := strconv.Atoi(get("SEND_MIN_INTERVAL_MS", "1000"))
	if err != nil || interval < 0 || interval > 60000 {
		errs = append(errs, errors.New("SEND_MIN_INTERVAL_MS must be between 0 and 60000"))
	}
	cfg.SendMinInterval = time.Duration(interval) * time.Millisecond

	switch cfg.DefaultLanguage {
	case "en", "pt-BR":
	default:
		errs = append(errs, errors.New("DEFAULT_LANGUAGE must be en or pt-BR"))
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, errors.New("LOG_LEVEL must be debug, info, warn or error"))
	}

	return cfg, errors.Join(errs...)
}
