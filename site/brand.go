package site

import _ "embed"

//go:embed assets/x9-logo.svg
var logo []byte

//go:embed assets/x9-icon.svg
var favicon []byte

//go:embed x9.css
var css []byte

// Logo returns the approved adaptive X-9 v11 logo.
func Logo() []byte { return logo }

// Favicon returns the approved adaptive X-9 v11 icon.
func Favicon() []byte { return favicon }

// CSS returns X-9's static-site stylesheet.
func CSS() []byte { return css }
