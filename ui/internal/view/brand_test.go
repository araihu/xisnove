package view

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestBrandURLsPinEmbeddedBytes(t *testing.T) {
	for _, asset := range []struct{ url, body string }{
		{BrandIconURL(), BrandIconSVG()},
		{BrandThemeURL(), BrandThemeCSS()},
	} {
		got := fmt.Sprintf("%x", sha256.Sum256([]byte(asset.body)))
		if asset.body == "" || !strings.Contains(asset.url, got) {
			t.Fatalf("brand URL %s does not pin canonical bytes %s", asset.url, got)
		}
	}
}
