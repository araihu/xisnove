package command

import (
	"net/http"
	"sort"
	"strings"

	"github.com/araihu/xisnove/cli/internal/config"
	"github.com/araihu/xisnove/cli/internal/credential"
	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/spf13/cobra"
)

type profileView struct {
	Current    bool                 `json:"current"`
	Name       string               `json:"name"`
	URL        string               `json:"url"`
	Credential config.CredentialRef `json:"credential"`
}

func newProfileCommand(runtime Runtime) *cobra.Command {
	profile := &cobra.Command{Use: "profile", Short: "Manage named server and credential profiles"}
	profile.AddCommand(
		newProfileSetCommand(runtime),
		newProfileListCommand(runtime),
		newProfileShowCommand(runtime),
		newProfileUseCommand(runtime),
		newProfileDeleteCommand(runtime),
	)
	return profile
}

func newProfileSetCommand(runtime Runtime) *cobra.Command {
	var serverURL, mode, reference string
	cmd := &cobra.Command{
		Use:   "set NAME",
		Short: "Create or update a named profile",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return problem.Usage("profile set requires exactly one NAME")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return problem.Usage("profile NAME must not be empty")
			}
			if serverURL == "" {
				return problem.Usage("--url is required")
			}
			normalizedURL, err := config.NormalizeServerURL(serverURL)
			if err != nil {
				return problem.Usage("--url " + err.Error())
			}
			credentialMode := config.CredentialMode(mode)
			if reference == "" {
				if credentialMode != config.CredentialKeyring {
					return problem.Usage("--credential-ref is required for " + mode + " mode")
				}
				reference = credential.DefaultReference(name).Reference
			}
			credentialRef := config.CredentialRef{Mode: credentialMode, Reference: reference}
			if err := credentialRef.Validate(); err != nil {
				return problem.Usage(err.Error())
			}
			cfg, err := loadConfig(runtime)
			if err != nil {
				return err
			}
			cfg.Profiles[name] = config.Profile{
				URL:        normalizedURL,
				Credential: credentialRef,
			}
			cfg.CurrentProfile = name
			if err := (config.Store{Path: *runtime.ConfigPath}).Save(cfg); err != nil {
				return localFailure("save profile", err)
			}
			return renderProfiles(runtime, []profileView{{Current: true, Name: name, URL: normalizedURL, Credential: cfg.Profiles[name].Credential}})
		},
	}
	cmd.Flags().StringVar(&serverURL, "url", "", "Xisnove control-plane URL")
	cmd.Flags().StringVar(&mode, "credential-mode", string(config.CredentialKeyring), "credential source: keyring, env, or file")
	cmd.Flags().StringVar(&reference, "credential-ref", "", "keyring account, environment variable, or absolute file path")
	return cmd
}

func newProfileListCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, err := loadConfig(runtime)
			if err != nil {
				return err
			}
			views := make([]profileView, 0, len(cfg.Profiles))
			for name, profile := range cfg.Profiles {
				views = append(views, profileView{Current: name == cfg.CurrentProfile, Name: name, URL: profile.URL, Credential: profile.Credential})
			}
			sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
			return renderProfiles(runtime, views)
		},
	}
}

func newProfileShowCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "show [NAME]",
		Short: "Show one profile",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return problem.Usage("profile show accepts at most one NAME")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := loadConfig(runtime)
			if err != nil {
				return err
			}
			name := cfg.CurrentProfile
			if len(args) == 1 {
				name = args[0]
			}
			profile, ok := cfg.Profiles[name]
			if !ok {
				return problem.Local(http.StatusNotFound, "Profile not found", "profile "+name+" does not exist", "profile_not_found")
			}
			return renderProfiles(runtime, []profileView{{Current: name == cfg.CurrentProfile, Name: name, URL: profile.URL, Credential: profile.Credential}})
		},
	}
}

func newProfileUseCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "use NAME",
		Short: "Select the default profile",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return problem.Usage("profile use requires exactly one NAME")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := loadConfig(runtime)
			if err != nil {
				return err
			}
			profile, ok := cfg.Profiles[args[0]]
			if !ok {
				return problem.Local(http.StatusNotFound, "Profile not found", "profile "+args[0]+" does not exist", "profile_not_found")
			}
			cfg.CurrentProfile = args[0]
			if err := (config.Store{Path: *runtime.ConfigPath}).Save(cfg); err != nil {
				return localFailure("select profile", err)
			}
			return renderProfiles(runtime, []profileView{{Current: true, Name: args[0], URL: profile.URL, Credential: profile.Credential}})
		},
	}
}

func newProfileDeleteCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a profile without deleting its referenced credential",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return problem.Usage("profile delete requires exactly one NAME")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := loadConfig(runtime)
			if err != nil {
				return err
			}
			if _, ok := cfg.Profiles[args[0]]; !ok {
				return problem.Local(http.StatusNotFound, "Profile not found", "profile "+args[0]+" does not exist", "profile_not_found")
			}
			delete(cfg.Profiles, args[0])
			if cfg.CurrentProfile == args[0] {
				names := make([]string, 0, len(cfg.Profiles))
				for name := range cfg.Profiles {
					names = append(names, name)
				}
				sort.Strings(names)
				cfg.CurrentProfile = ""
				if len(names) > 0 {
					cfg.CurrentProfile = names[0]
				}
			}
			if err := (config.Store{Path: *runtime.ConfigPath}).Save(cfg); err != nil {
				return localFailure("delete profile", err)
			}
			return renderProfiles(runtime, []profileView{})
		},
	}
}

func loadConfig(runtime Runtime) (config.Config, error) {
	cfg, err := (config.Store{Path: *runtime.ConfigPath}).Load()
	if err != nil {
		return config.Config{}, localFailure("load profiles", err)
	}
	return cfg, nil
}

func renderProfiles(runtime Runtime, views []profileView) error {
	rows := make([][]string, 0, len(views))
	for _, view := range views {
		current := ""
		if view.Current {
			current = "*"
		}
		rows = append(rows, []string{current, view.Name, view.URL, string(view.Credential.Mode), view.Credential.Reference})
	}
	var value any = views
	if len(views) == 1 {
		value = views[0]
	}
	return (output.Renderer{Writer: runtime.Stdout, Format: output.Format(*runtime.OutputFormat)}).Render(value, output.Table{
		Headers: []string{"CURRENT", "NAME", "URL", "CREDENTIAL", "REFERENCE"},
		Rows:    rows,
	})
}

func localFailure(action string, err error) *problem.Error {
	return problem.Local(http.StatusInternalServerError, "Local CLI operation failed", action+": "+err.Error(), "local_cli_error")
}
