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

type Profile struct {
	Name       string
	URL        string
	Credential config.CredentialRef
}

type Resolver struct {
	Store       config.Store
	Credentials credential.Resolver
}

func (r Resolver) Open(profileOverride string) (Session, error) {
	profile, err := r.Profile(profileOverride)
	if err != nil {
		return Session{}, err
	}
	token, err := r.Credentials.Lookup(profile.Credential)
	if err != nil {
		return Session{}, fmt.Errorf("resolve credential for profile %q: %w", profile.Name, err)
	}
	return Session{Name: profile.Name, URL: profile.URL, Token: token}, nil
}

func (r Resolver) Profile(profileOverride string) (Profile, error) {
	cfg, err := r.Store.Load()
	if err != nil {
		return Profile{}, fmt.Errorf("load profiles: %w", err)
	}
	name := profileOverride
	if name == "" {
		name = cfg.CurrentProfile
	}
	profile, ok := cfg.Profiles[name]
	if name == "" || !ok {
		return Profile{}, fmt.Errorf("%w: select one with --profile or `xisnove profile use NAME`", ErrProfileNotFound)
	}
	return Profile{Name: name, URL: profile.URL, Credential: profile.Credential}, nil
}
