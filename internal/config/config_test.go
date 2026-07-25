package config_test

import (
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/config"
)

func TestLoadUsesSafeDefaults(t *testing.T) {
	cfg, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.DatabasePath != "xisnove.db" {
		t.Fatalf("DatabasePath = %q", cfg.DatabasePath)
	}
	if cfg.LeaseDuration != 30*time.Second {
		t.Fatalf("LeaseDuration = %s", cfg.LeaseDuration)
	}
	if cfg.SessionDuration != 12*time.Hour {
		t.Fatalf("SessionDuration = %s", cfg.SessionDuration)
	}
}

func TestLoadRejectsNonPositiveLeaseDuration(t *testing.T) {
	_, err := config.Load(func(key string) string {
		if key == "XISNOVE_LEASE_DURATION" {
			return "0s"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected invalid lease duration")
	}
}
