package view

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
)

// x9LogoSVG is the approved Arai Hû v11 export. It stays inline so the
// Goshtoso theme's .dark class can provide the surface, ink, and signal tokens.
//
//go:embed static/x9-logo-transparent.svg
var x9LogoSVG string

//go:embed static/araihu.css
var brandThemeCSS string

//go:embed static/x9-icon.svg
var brandIconSVG string

// BrandThemeURL and BrandIconURL change whenever their embedded bytes change.
// Older immutable URLs remain served separately by the web package.
func BrandThemeURL() string {
	return fmt.Sprintf("/ui/araihu-%x.css", sha256.Sum256([]byte(brandThemeCSS)))
}
func BrandIconURL() string {
	return fmt.Sprintf("/ui/x9-icon-%x.svg", sha256.Sum256([]byte(brandIconSVG)))
}
func BrandThemeCSS() string { return brandThemeCSS }
func BrandIconSVG() string  { return brandIconSVG }
