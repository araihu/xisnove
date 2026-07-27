package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBootstrapPasswordUsesPrivateRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte("correct horse battery staple\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	password, err := readBootstrapPassword(context.Background(), path)
	if err != nil || password != "correct horse battery staple" {
		t.Fatalf("readBootstrapPassword() = %q, %v", password, err)
	}
}

func TestReadBootstrapPasswordRejectsUnsafeFileWithoutLeakingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sensitive-password-name")
	if err := os.WriteFile(path, []byte("must-not-leak"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readBootstrapPassword(context.Background(), path)
	if err == nil {
		t.Fatal("readBootstrapPassword() error = nil")
	}
	if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("readBootstrapPassword() leaked sensitive detail: %v", err)
	}
}

func TestBootstrapRejectsUnboundedCommandTimeoutBeforeDatabaseOpen(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "bootstrap.db")
	err := bootstrapCommand(context.Background(), []string{
		"--database-url", databasePath, "--email", "admin@example.test",
		"--password-file", filepath.Join(t.TempDir(), "unused"), "--timeout", "0s",
	})
	if err == nil || strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("zero command timeout error = %v", err)
	}
	if _, statErr := os.Stat(databasePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid input created database: %v", statErr)
	}
}
