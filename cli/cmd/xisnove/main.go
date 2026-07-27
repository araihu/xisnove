package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/araihu/xisnove/cli/internal/buildinfo"
	"github.com/araihu/xisnove/cli/internal/command"
)

func main() {
	runner := command.Runner{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	os.Exit(execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr, runner.Run))
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer, run func(context.Context, []string) int) int {
	if len(args) > 0 && args[0] == "--version" {
		if len(args) != 1 {
			fmt.Fprintln(stderr, "error: --version accepts no arguments")
			return 2
		}
		value, err := buildinfo.String("xisnove")
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, value)
		return 0
	}
	return run(ctx, args)
}
