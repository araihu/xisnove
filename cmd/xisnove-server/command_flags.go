package main

import (
	"flag"
	"fmt"
	"io"
)

type commandUsageError struct{ err error }

func (e *commandUsageError) Error() string { return e.err.Error() }
func (e *commandUsageError) Unwrap() error { return e.err }

func newCommandUsageError(err error) error {
	if err == nil {
		return nil
	}
	return &commandUsageError{err: err}
}

func newCommandFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func parseCommandFlags(flags *flag.FlagSet, args []string) error {
	if err := flags.Parse(args); err != nil {
		return newCommandUsageError(err)
	}
	if flags.NArg() != 0 {
		return newCommandUsageError(fmt.Errorf("unexpected arguments: %v", flags.Args()))
	}
	return nil
}
