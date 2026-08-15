// Command x9-site builds the static X-9 product site.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/xisnove/site"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "build" {
		fmt.Fprintln(os.Stderr, "usage: x9-site build")
		os.Exit(2)
	}
	if err := build(); err != nil {
		fmt.Fprintf(os.Stderr, "build site: %v\n", err)
		os.Exit(1)
	}
}

func build() error {
	assetsDir := filepath.Join("public", "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		return err
	}
	goshtosoCSS, err := assets.StylesCSS()
	if err != nil {
		return err
	}
	for name, content := range map[string][]byte{
		"styles.css":  goshtosoCSS,
		"x9.css":      site.CSS(),
		"x9-logo.svg": site.Logo(),
		"x9-icon.svg": site.Favicon(),
	} {
		if err := os.WriteFile(filepath.Join(assetsDir, name), content, 0o644); err != nil {
			return err
		}
	}
	file, err := os.Create(filepath.Join("public", "index.html"))
	if err != nil {
		return err
	}
	defer file.Close()
	return site.Page().Render(context.Background(), file)
}
