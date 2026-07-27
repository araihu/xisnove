package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/buildinfo"
)

func main() {
	os.Exit(execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr, run))
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer, command func(context.Context, []string) error) int {
	if len(args) > 0 && args[0] == "--version" {
		if len(args) != 1 {
			fmt.Fprintln(stderr, "error: --version accepts no arguments")
			return 2
		}
		value, err := buildinfo.String("xisnove-server")
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, value)
		return 0
	}
	if err := command(ctx, args); err != nil {
		fmt.Fprintln(stderr, err)
		if len(args) == 0 || strings.HasPrefix(args[0], "-") || !knownCommand(args) {
			return 2
		}
		if migration.Retryable(err) {
			return 75
		}
		return 1
	}
	return 0
}

func knownCommand(args []string) bool {
	return args[0] == "serve" ||
		(len(args) >= 2 && args[0] == "db" && (args[1] == "migrate" || args[1] == "backup")) ||
		(len(args) >= 2 && args[0] == "admin" && args[1] == "bootstrap") ||
		(len(args) >= 3 && args[0] == "notifications" && args[1] == "keys" && args[2] == "rotate")
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: xisnove-server <db migrate|db backup|admin bootstrap|notifications keys rotate|serve>")
	}
	switch {
	case len(args) >= 2 && args[0] == "db" && args[1] == "migrate":
		return migrateCommand(ctx, args[2:])
	case len(args) >= 2 && args[0] == "db" && args[1] == "backup":
		return backupCommand(ctx, args[2:])
	case len(args) >= 2 && args[0] == "admin" && args[1] == "bootstrap":
		return bootstrapCommand(ctx, args[2:])
	case len(args) >= 3 && args[0] == "notifications" && args[1] == "keys" && args[2] == "rotate":
		return rotateNotificationKeysCommand(ctx, args[3:])
	case args[0] == "serve":
		return serveCommand(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command")
	}
}
