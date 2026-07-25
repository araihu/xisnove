package config

import (
	"fmt"
	"time"
)

type Config struct {
	ListenAddr      string
	DatabasePath    string
	LeaseDuration   time.Duration
	SessionDuration time.Duration
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		ListenAddr:      valueOr(getenv("XISNOVE_LISTEN_ADDR"), "127.0.0.1:8080"),
		DatabasePath:    valueOr(getenv("XISNOVE_DATABASE_PATH"), "xisnove.db"),
		LeaseDuration:   30 * time.Second,
		SessionDuration: 12 * time.Hour,
	}

	var err error
	if raw := getenv("XISNOVE_LEASE_DURATION"); raw != "" {
		cfg.LeaseDuration, err = time.ParseDuration(raw)
		if err != nil || cfg.LeaseDuration <= 0 {
			return Config{}, fmt.Errorf("XISNOVE_LEASE_DURATION must be positive: %q", raw)
		}
	}
	if raw := getenv("XISNOVE_SESSION_DURATION"); raw != "" {
		cfg.SessionDuration, err = time.ParseDuration(raw)
		if err != nil || cfg.SessionDuration <= 0 {
			return Config{}, fmt.Errorf("XISNOVE_SESSION_DURATION must be positive: %q", raw)
		}
	}

	return cfg, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
