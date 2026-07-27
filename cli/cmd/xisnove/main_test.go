package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/araihu/xisnove/cli/internal/buildinfo"
)

func TestExecuteVersionSkipsRunner(t *testing.T) {
	setCLIBuildInfo(t, "1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-27T03:04:05Z", "false")
	var stdout, stderr bytes.Buffer
	called := false
	exit := execute(context.Background(), []string{"--version"}, &stdout, &stderr, func(context.Context, []string) int {
		called = true
		return 0
	})
	if exit != 0 || called || stderr.Len() != 0 {
		t.Fatalf("execute = exit %d called %t stderr %q", exit, called, stderr.String())
	}
	want := "xisnove version=1.2.3 commit=0123456789abcdef0123456789abcdef01234567 build_date=2026-07-27T03:04:05Z dirty=false\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestExecuteInvalidVersionAndMalformedVersionFlagsUseSingleUsageDiagnostic(t *testing.T) {
	for _, arguments := range [][]string{{"--version"}, {"--version", "extra"}} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			setCLIBuildInfo(t, "dev", "bad", "bad", "true")
			var stdout, stderr bytes.Buffer
			exit := execute(context.Background(), arguments, &stdout, &stderr, func(context.Context, []string) int {
				t.Fatal("CLI runner initialized")
				return 0
			})
			if exit != 2 || stdout.Len() != 0 || strings.Count(stderr.String(), "\n") != 1 {
				t.Fatalf("execute = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
			}
		})
	}
}

func setCLIBuildInfo(t *testing.T, version, commit, date, dirty string) {
	t.Helper()
	oldVersion, oldCommit, oldDate, oldDirty := buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty
	buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty = version, commit, date, dirty
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty = oldVersion, oldCommit, oldDate, oldDirty
	})
}
