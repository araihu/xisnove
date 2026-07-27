// Package observability serves the Agent's bounded health and metrics surface.
package observability

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

const ShutdownTimeout = 10 * time.Second

type State struct {
	credentialLoaded  atomic.Bool
	clientInitialized atomic.Bool
	acceptingLeases   atomic.Bool
	draining          atomic.Bool
}

func NewState() *State { return &State{} }

func (state *State) MarkCredentialLoaded()  { state.credentialLoaded.Store(true) }
func (state *State) MarkClientInitialized() { state.clientInitialized.Store(true) }

func (state *State) SetAcceptingLeases(accepting bool) {
	if !accepting || state.draining.Load() {
		state.acceptingLeases.Store(false)
		return
	}
	state.acceptingLeases.Store(true)
	if state.draining.Load() {
		state.acceptingLeases.Store(false)
	}
}

func (state *State) BeginDrain() {
	state.draining.Store(true)
	state.acceptingLeases.Store(false)
}

func (state *State) Ready() bool {
	return state.credentialLoaded.Load() &&
		state.clientInitialized.Load() &&
		state.acceptingLeases.Load() &&
		!state.draining.Load()
}

func Handler(state *State) http.Handler {
	if state == nil {
		state = NewState()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /readyz", func(writer http.ResponseWriter, _ *http.Request) {
		if !state.Ready() {
			http.Error(writer, "not ready", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /metrics", func(writer http.ResponseWriter, _ *http.Request) {
		ready, accepting, draining := 0, 0, 0
		if state.Ready() {
			ready = 1
		}
		if state.acceptingLeases.Load() {
			accepting = 1
		}
		if state.draining.Load() {
			draining = 1
		}
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = fmt.Fprintf(writer, "# TYPE xisnove_agent_ready gauge\nxisnove_agent_ready %d\n# TYPE xisnove_agent_accepting_leases gauge\nxisnove_agent_accepting_leases %d\n# TYPE xisnove_agent_draining gauge\nxisnove_agent_draining %d\n", ready, accepting, draining)
	})
	return mux
}

type Server struct {
	listener net.Listener
	server   *http.Server
	state    *State
}

func Listen(address string, state *State) (*Server, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen for Agent observability: %w", err)
	}
	if state == nil {
		state = NewState()
	}
	return &Server{
		listener: listener,
		server: &http.Server{
			Handler:           Handler(state),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       5 * time.Second,
			WriteTimeout:      5 * time.Second,
			IdleTimeout:       30 * time.Second,
			MaxHeaderBytes:    8 << 10,
		},
		state: state,
	}, nil
}

func (server *Server) Address() string { return server.listener.Addr().String() }

func (server *Server) Serve(ctx context.Context) error {
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.server.Serve(server.listener) }()

	select {
	case err := <-serveDone:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve Agent observability: %w", err)
	case <-ctx.Done():
		server.state.BeginDrain()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()
		if err := server.server.Shutdown(shutdownCtx); err != nil {
			_ = server.server.Close()
			return fmt.Errorf("shutdown Agent observability: %w", err)
		}
		err := <-serveDone
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve Agent observability: %w", err)
		}
		return nil
	}
}
