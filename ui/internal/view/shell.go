package view

import (
	"bytes"
	"context"
	"html"
	"io"

	"github.com/a-h/templ"
	"github.com/araihu/goshtoso-app-shells/consoleshell"
	"github.com/araihu/goshtoso-charts/components/dependencies"
	"github.com/araihu/goshtoso/components/sidebar"
	"github.com/araihu/xisnove/ui/internal/seasonalassets"
)

func ConsolePage(title, csrfToken string, content templ.Component) templ.Component {
	page := consoleshell.Page{
		Title:         title,
		DocumentTitle: title + " · X-9",
		Active:        "nav-monitors",
		Content:       content,
		Head:          consoleHead(),
	}
	return nonceConsole(consoleshell.Layout(consoleConfig(csrfToken), page))
}

func consoleHead() templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
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
		Active:        "nav-monitors",
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
				{ID: "nav-incidents", Label: "Incidents", Disabled: true, Badge: "Soon"},
				{ID: "nav-agents", Label: "Agents", Disabled: true, Badge: "Soon"},
			}}},
		},
		Appearance: consoleshell.AppearanceConfig{
			DefaultTheme:       "araihu",
			InitialColorScheme: consoleshell.ColorSchemeSystem,
			PersistPreferences: true,
			ThemeStylesheets:   []string{"/ui/araihu-f841fe90.css"},
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
