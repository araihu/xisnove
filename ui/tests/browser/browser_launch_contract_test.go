//go:build browser

package browser_test

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestAppendMacAppCodeSignCloneFeaturePreservesChromedpDefaults(t *testing.T) {
	command := exec.Command("chromium", "--disable-features=site-per-process,Translate")

	appendMacAppCodeSignCloneFeature(command)

	want := []string{"chromium", "--disable-features=site-per-process,Translate,MacAppCodeSignClone"}
	if !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("args = %q, want %q", command.Args, want)
	}
}

func TestAppendMacAppCodeSignCloneFeatureAddsMissingSwitch(t *testing.T) {
	command := exec.Command("chromium", "--headless")

	appendMacAppCodeSignCloneFeature(command)

	want := []string{"chromium", "--headless", "--disable-features=MacAppCodeSignClone"}
	if !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("args = %q, want %q", command.Args, want)
	}
}

func TestAppendMacAppCodeSignCloneFeatureIsIdempotent(t *testing.T) {
	command := exec.Command("chromium", "--disable-features=MacAppCodeSignClone")

	appendMacAppCodeSignCloneFeature(command)
	appendMacAppCodeSignCloneFeature(command)

	want := []string{"chromium", "--disable-features=MacAppCodeSignClone"}
	if !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("args = %q, want %q", command.Args, want)
	}
}

func TestDarwinBrowserCandidatesPrioritizeHomebrewChromium(t *testing.T) {
	candidates := browserCandidates("darwin")
	if len(candidates) == 0 || candidates[0] != "/opt/homebrew/bin/chromium" {
		t.Fatalf("browser candidates = %v, want Homebrew Chromium first", candidates)
	}
}
