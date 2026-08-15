package view

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/a-h/templ"
	"github.com/araihu/goshtoso-app-shells/consoleshell"
	"github.com/araihu/goshtoso-charts/components/dependencies"
	"github.com/araihu/goshtoso/components/head"
	"github.com/araihu/goshtoso/components/sidebar"
	"github.com/araihu/xisnove/ui/internal/seasonalassets"
)

func ConsolePage(title, csrfToken string, content templ.Component) templ.Component {
	metadata := documentMetadata(title, consoleRoute(title), consoleDescription(title))
	page := consoleshell.Page{
		Title:         title,
		DocumentTitle: metadata.Title,
		Description:   metadata.Description,
		CanonicalURL:  metadata.CanonicalURL,
		Active:        consoleActive(title),
		Content:       content,
		Head:          consoleHead(metadata),
	}
	return nonceConsole(consoleshell.Layout(consoleConfig(csrfToken), page))
}

func consoleSocialMetadata(metadata head.MetadataConfig) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		// consoleshell owns title, description, and canonical tags. Emit the
		// remaining Goshtoso metadata here so each initial document has one copy.
		values := []string{
			`<meta property="og:url" content="` + html.EscapeString(metadata.CanonicalURL) + `">`,
			`<meta property="og:type" content="` + html.EscapeString(string(metadata.OpenGraphType)) + `">`,
			`<meta property="og:title" content="` + html.EscapeString(metadata.Title) + `">`,
			`<meta property="og:description" content="` + html.EscapeString(metadata.Description) + `">`,
			`<meta property="og:site_name" content="` + html.EscapeString(metadata.SiteName) + `">`,
			`<meta property="og:locale" content="` + html.EscapeString(metadata.Locale) + `">`,
			`<meta property="og:image" content="` + html.EscapeString(metadata.Image.URL) + `">`,
			`<meta property="og:image:type" content="` + html.EscapeString(metadata.Image.MIMEType) + `">`,
			fmt.Sprintf(`<meta property="og:image:width" content="%d">`, metadata.Image.Width),
			fmt.Sprintf(`<meta property="og:image:height" content="%d">`, metadata.Image.Height),
			`<meta property="og:image:alt" content="` + html.EscapeString(metadata.Image.Alt) + `">`,
			`<meta name="twitter:card" content="` + html.EscapeString(string(metadata.TwitterCard)) + `">`,
			`<meta name="twitter:title" content="` + html.EscapeString(metadata.Title) + `">`,
			`<meta name="twitter:description" content="` + html.EscapeString(metadata.Description) + `">`,
			`<meta name="twitter:image" content="` + html.EscapeString(metadata.Image.URL) + `">`,
			`<meta name="twitter:image:alt" content="` + html.EscapeString(metadata.Image.Alt) + `">`,
		}
		for _, value := range values {
			if _, err := io.WriteString(writer, value); err != nil {
				return err
			}
		}
		return nil
	})
}

func consoleRoute(title string) string {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "monitors":
		return "/monitors"
	case "locations":
		return "/locations"
	default:
		return "/problem"
	}
}

func consoleDescription(title string) string {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "monitors":
		return "Inspect Xisnove monitor health, lifecycle, provenance, and bounded state history."
	case "locations":
		return "Manage reusable monitoring locations and their lifecycle."
	}
	return "Xisnove operator workspace."
}

func consoleActive(title string) string {
	if strings.EqualFold(strings.TrimSpace(title), "locations") {
		return "nav-locations"
	}
	return "nav-monitors"
}

func consoleHead(metadata head.MetadataConfig) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		if err := consoleSocialMetadata(metadata).Render(ctx, writer); err != nil {
			return err
		}
		if err := dependencies.Dependencies().Render(ctx, writer); err != nil {
			return err
		}
		return AppStyles().Render(ctx, writer)
	})
}

func ConsoleFragment(title, csrfToken string, content templ.Component) templ.Component {
	page := consoleshell.Page{
		Title:         title,
		DocumentTitle: title + " · X-9",
		Active:        consoleActive(title),
		Content:       content,
	}
	return nonceConsole(consoleshell.Fragment(consoleConfig(csrfToken), page))
}

func consoleConfig(csrfToken string) consoleshell.Config {
	return consoleshell.Config{
		Brand: consoleshell.Brand{
			Name:    "X-9",
			HomeURL: "/monitors",
			ManagedLogo: &consoleshell.ManagedBrandAsset{
				URL:    seasonalassets.LogoPath,
				Alt:    "X-9",
				Width:  120,
				Height: 32,
			},
			FaviconURL: seasonalassets.FaviconPath,
		},
		Navigation: consoleshell.Navigation{
			DisableSearch: true,
			SectionsTitle: "Monitoring",
			Sections: []sidebar.Section{{Items: []sidebar.Item{
				{ID: "nav-monitors", Label: "Monitors", Href: "/monitors"},
				{ID: "nav-locations", Label: "Locations", Href: "/locations"},
				{ID: "nav-incidents", Label: "Incidents", Disabled: true, Badge: "Soon"},
				{ID: "nav-agents", Label: "Agents", Disabled: true, Badge: "Soon"},
			}}},
		},
		Appearance: consoleshell.AppearanceConfig{
			DefaultTheme:       "araihu",
			InitialColorScheme: consoleshell.ColorSchemeSystem,
			PersistPreferences: true,
			ThemeStylesheets:   []string{"/ui/araihu-v0.2.1.css"},
		},
		Interactions: consoleshell.InteractionConfig{
			EnableHTMX: true,
		},
		SidebarHeader: ConsoleSidebarHeader(),
		HeaderActions: ConsoleHeaderActions(csrfToken),
		ModalSlot:     GlobalSearchModal(),
		BodyEnd:       ApplicationScript(),
		MainID:        "main-content",
		ContentID:     "console-content",
	}
}

func nonceConsole(component templ.Component) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		var body bytes.Buffer
		if err := component.Render(ctx, &body); err != nil {
			return err
		}
		nonce := templ.GetNonce(ctx)
		if nonce != "" {
			escapedNonce := html.EscapeString(nonce)
			bodyBytes := bytes.Replace(body.Bytes(), []byte("<script>"), []byte(`<script nonce="`+escapedNonce+`">`), 1)
			bodyBytes = bytes.Replace(bodyBytes, []byte(`<script defer src="/consoleshell/assets/shell.js`), []byte(`<script defer nonce="`+escapedNonce+`" src="/consoleshell/assets/shell.js`), 1)
			body.Reset()
			_, _ = body.Write(bodyBytes)
		}
		_, err := io.Copy(w, &body)
		return err
	})
}
