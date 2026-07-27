package contract_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalApacheLicenseAndNotice(t *testing.T) {
	license, err := os.ReadFile(filepath.Join(distributionRoot(t), "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(license)
	if got := fmt.Sprintf("%x", sha256.Sum256(license)); got != "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30" {
		t.Fatalf("LICENSE SHA-256 = %s, want canonical Apache 2.0 text", got)
	}
	for _, required := range []string{
		"Apache License\n                           Version 2.0, January 2004",
		"http://www.apache.org/licenses/",
		"END OF TERMS AND CONDITIONS",
		"Copyright [yyyy] [name of copyright owner]",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("LICENSE missing canonical Apache 2.0 text %q", required)
		}
	}

	notice, err := os.ReadFile(filepath.Join(distributionRoot(t), "NOTICE"))
	if err != nil {
		t.Fatal(err)
	}
	noticeText := string(notice)
	for _, required := range []string{"Xisnove", "Copyright 2026 AraiHu", "Apache License, Version 2.0"} {
		if !strings.Contains(noticeText, required) {
			t.Errorf("NOTICE missing %q", required)
		}
	}
}
