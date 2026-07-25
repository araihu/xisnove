package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/araihu/xisnove/cli/internal/config"
	"github.com/araihu/xisnove/cli/internal/credential"
	"github.com/araihu/xisnove/cli/internal/output"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/araihu/xisnove/cli/internal/session"
	"github.com/spf13/cobra"
)

type Family interface {
	Name() string
	Command(Runtime) *cobra.Command
}

type Runtime struct {
	Stdout          io.Writer
	Stderr          io.Writer
	ConfigPath      *string
	ProfileOverride *string
	OutputFormat    *string
	Credentials     credential.Resolver
}

func (r Runtime) OpenSession() (session.Session, error) {
	return (session.Resolver{
		Store:       config.Store{Path: *r.ConfigPath},
		Credentials: r.Credentials,
	}).Open(*r.ProfileOverride)
}

type Runner struct {
	Stdout      io.Writer
	Stderr      io.Writer
	Families    []Family
	Credentials credential.Resolver
}

func (r Runner) Run(ctx context.Context, args []string) int {
	stdout := r.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := r.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	state := rootState{stdout: stdout, stderr: stderr, configPath: defaultConfigPath(), outputFormat: string(output.TableFormat)}
	root := newRoot(&state, r.Families, r.Credentials)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	var typed *problem.Error
	if !errors.As(err, &typed) {
		if !state.executionStarted {
			typed = problem.Usage(err.Error())
		} else {
			typed = problem.Local(http.StatusInternalServerError, "CLI operation failed", err.Error(), "cli_error")
		}
	}
	if renderErr := renderProblem(stderr, output.Format(state.outputFormat), typed); renderErr != nil {
		_, _ = fmt.Fprintf(stderr, "error: %s\n", typed.Error())
	}
	return typed.ExitCode()
}

type rootState struct {
	stdout           io.Writer
	stderr           io.Writer
	configPath       string
	profileOverride  string
	outputFormat     string
	executionStarted bool
}

func newRoot(state *rootState, families []Family, credentials credential.Resolver) *cobra.Command {
	root := &cobra.Command{
		Use:           "xisnove",
		Short:         "Human client for the Xisnove control plane",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			switch output.Format(state.outputFormat) {
			case output.TableFormat, output.JSONFormat, output.YAMLFormat:
				state.executionStarted = true
				return nil
			default:
				return problem.Usage("unsupported output format " + state.outputFormat + "; use table, json, or yaml")
			}
		},
	}
	root.SetOut(state.stdout)
	root.SetErr(state.stderr)
	root.PersistentFlags().StringVar(&state.configPath, "config", state.configPath, "profile configuration file")
	root.PersistentFlags().StringVar(&state.profileOverride, "profile", "", "named profile override")
	root.PersistentFlags().StringVarP(&state.outputFormat, "output", "o", state.outputFormat, "output format: table, json, or yaml")
	runtime := Runtime{
		Stdout:          state.stdout,
		Stderr:          state.stderr,
		ConfigPath:      &state.configPath,
		ProfileOverride: &state.profileOverride,
		OutputFormat:    &state.outputFormat,
		Credentials:     credentials,
	}
	root.AddCommand(newProfileCommand(runtime))
	if len(families) == 0 {
		for _, name := range []string{"auth", "monitor", "location", "agent", "incident", "notification", "discovery", "status"} {
			root.AddCommand(unavailableCommand(name))
		}
	} else {
		for _, family := range families {
			root.AddCommand(family.Command(runtime))
		}
	}
	return root
}

func unavailableCommand(name string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: name + " workflows",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return problem.ContractUnavailable(name)
		},
	}
}

func renderProblem(writer io.Writer, format output.Format, typed *problem.Error) error {
	switch format {
	case output.JSONFormat, output.YAMLFormat:
		return (output.Renderer{Writer: writer, Format: format}).Render(typed, output.Table{})
	default:
		_, err := fmt.Fprintf(writer, "error: %s\n", typed.Error())
		return err
	}
}

func defaultConfigPath() string {
	if override := os.Getenv("XISNOVE_CONFIG"); override != "" {
		return override
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(".", ".xisnove", "config.yaml")
	}
	return filepath.Join(dir, "xisnove", "config.yaml")
}
