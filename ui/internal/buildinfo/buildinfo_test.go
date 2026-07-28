package buildinfo

import "testing"

func TestStringFormatsValidatedReleaseMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate, oldDirty := Version, Commit, BuildDate, Dirty
	Version, Commit, BuildDate, Dirty = "1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-27T03:04:05Z", "false"
	t.Cleanup(func() { Version, Commit, BuildDate, Dirty = oldVersion, oldCommit, oldDate, oldDirty })
	got, err := String("xisnove-ui")
	if err != nil {
		t.Fatal(err)
	}
	want := "xisnove-ui version=1.2.3 commit=0123456789abcdef0123456789abcdef01234567 build_date=2026-07-27T03:04:05Z dirty=false"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestStringRejectsInvalidMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate, oldDirty := Version, Commit, BuildDate, Dirty
	Version, Commit, BuildDate, Dirty = "dev", "unknown", "now", "true"
	t.Cleanup(func() { Version, Commit, BuildDate, Dirty = oldVersion, oldCommit, oldDate, oldDirty })
	if _, err := String("xisnove-ui"); err == nil {
		t.Fatal("String() error = nil")
	}
}
