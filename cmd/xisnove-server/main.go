package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: xisnove-server <db migrate|admin bootstrap|serve>")
	}
	switch {
	case args[0] == "db" && args[1] == "migrate":
		return migrateCommand(ctx, args[2:])
	case args[0] == "admin" && args[1] == "bootstrap":
		return bootstrapCommand(ctx, args[2:])
	case args[0] == "serve":
		return serveCommand(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command")
	}
}
