package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/araihu/xisnove/internal/mockapi"
)

type options struct {
	listen string
}

func parseOptions(args []string) (options, error) {
	set := flag.NewFlagSet("xisnove-mock", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	var result options
	set.StringVar(&result.listen, "listen", "127.0.0.1:8089", "HTTP listen address")
	if err := set.Parse(args); err != nil {
		return options{}, err
	}
	if set.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", set.Args())
	}
	return result, nil
}

func main() {
	config, err := parseOptions(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, config); err != nil {
		slog.Error("mock server stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config options) error {
	server := &http.Server{
		Addr:              config.listen,
		Handler:           mockapi.NewServer().Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	result := make(chan error, 1)
	go func() {
		slog.Info("Xisnove mock listening", "address", "http://"+config.listen)
		result <- server.ListenAndServe()
	}()

	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		err := <-result
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
