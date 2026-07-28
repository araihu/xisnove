package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWritesStandaloneSite(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })

	if err := build(); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join("public", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Observe from outside", "https://github.com/araihu/xisnove", `class="x9-nav-icon" aria-hidden="true"`,
		`class="x9-skip" href="#main-content"`, "X9 · 200",
	} {
		if !strings.Contains(string(page), want) {
			t.Errorf("generated page misses %q", want)
		}
	}
	for _, asset := range []string{"styles.css", "x9.css", "x9-logo.svg", "x9-icon.svg"} {
		if info, err := os.Stat(filepath.Join("public", "assets", asset)); err != nil || info.Size() == 0 {
			t.Errorf("generated asset %s missing or empty: %v", asset, err)
		}
	}
	for _, legacy := range []string{"xisnove-logo.svg", "xisnove-favicon.svg"} {
		if _, err := os.Stat(filepath.Join("public", "assets", legacy)); !os.IsNotExist(err) {
			t.Errorf("generated site retains legacy asset %s", legacy)
		}
	}
}
