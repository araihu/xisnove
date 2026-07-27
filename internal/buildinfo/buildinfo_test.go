package buildinfo

import "testing"

func TestStringFormatsValidatedReleaseMetadata(t *testing.T) {
	setTestMetadata(t, "1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-27T03:04:05Z", "false")
	got, err := String("xisnove-server")
	if err != nil {
		t.Fatal(err)
	}
	want := "xisnove-server version=1.2.3 commit=0123456789abcdef0123456789abcdef01234567 build_date=2026-07-27T03:04:05Z dirty=false"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestStringRejectsInvalidReleaseMetadata(t *testing.T) {
	tests := []struct {
		name                         string
		version, commit, date, dirty string
	}{
		{"version", "v1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-27T03:04:05Z", "false"},
		{"commit", "1.2.3", "abc", "2026-07-27T03:04:05Z", "false"},
		{"date", "1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-27", "false"},
		{"non UTC date", "1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-27T00:04:05-03:00", "false"},
		{"dirty", "1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-27T03:04:05Z", "true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setTestMetadata(t, test.version, test.commit, test.date, test.dirty)
			if _, err := String("xisnove-server"); err == nil {
				t.Fatal("String() error = nil")
			}
		})
	}
}

func setTestMetadata(t *testing.T, version, commit, date, dirty string) {
	t.Helper()
	oldVersion, oldCommit, oldDate, oldDirty := Version, Commit, BuildDate, Dirty
	Version, Commit, BuildDate, Dirty = version, commit, date, dirty
	t.Cleanup(func() { Version, Commit, BuildDate, Dirty = oldVersion, oldCommit, oldDate, oldDirty })
}
