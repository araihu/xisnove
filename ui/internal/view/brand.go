package view

import _ "embed"

// x9LogoSVG is the approved Arai Hû v11 export. It stays inline so the
// Goshtoso theme's .dark class can provide the surface, ink, and signal tokens.
//
//go:embed static/x9-logo-transparent.svg
var x9LogoSVG string
