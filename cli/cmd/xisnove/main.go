package main

import (
	"context"
	"os"

	"github.com/araihu/xisnove/cli/internal/command"
)

func main() {
	runner := command.Runner{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	os.Exit(runner.Run(context.Background(), os.Args[1:]))
}
