package session

import (
	"errors"
	"fmt"

	"github.com/araihu/xisnove/cli/internal/config"
	"github.com/araihu/xisnove/cli/internal/credential"
)

var ErrProfileNotFound = errors.New("profile not found")

type Session struct {
	Name  string
	URL   string
	Token string
}

type Resolver struct {
	Store       config.Store
	Credentials credential.Resolver
}

func (r Resolver) Open(profileOverride string) (Session, error) {
	cfg, err := r.Store.Load()
	if err != nil {
		return Session{}, fmt.Errorf("load profiles: %w", err)
	}
	name := profileOverride
	if name == "" {
		name = cfg.CurrentProfile
	}
	profile, ok := cfg.Profiles[name]
	if name == "" || !ok {
		return Session{}, fmt.Errorf("%w: select one with --profile or `xisnove profile use NAME`", ErrProfileNotFound)
	}
	token, err := r.Credentials.Lookup(profile.Credential)
	if err != nil {
		return Session{}, fmt.Errorf("resolve credential for profile %q: %w", name, err)
	}
	return Session{Name: name, URL: profile.URL, Token: token}, nil
}
