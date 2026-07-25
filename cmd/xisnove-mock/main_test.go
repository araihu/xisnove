package main

import "testing"

func TestParseOptionsUsesSafeLoopbackDefaultAndExplicitOverride(t *testing.T) {
	defaults, err := parseOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.listen != "127.0.0.1:8089" {
		t.Fatalf("default listen = %q", defaults.listen)
	}

	override, err := parseOptions([]string{"-listen", "0.0.0.0:9090"})
	if err != nil {
		t.Fatal(err)
	}
	if override.listen != "0.0.0.0:9090" {
		t.Fatalf("override listen = %q", override.listen)
	}
}
