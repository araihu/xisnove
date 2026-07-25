package domain

import (
	"errors"
	"strings"
	"time"
)

var ErrInvalidLocation = errors.New("invalid location")

type Location struct {
	ID        LocationID
	Name      string
	CreatedAt time.Time
}

func NewLocation(id LocationID, name string, createdAt time.Time) (Location, error) {
	name = strings.TrimSpace(name)
	if id == "" || name == "" {
		return Location{}, ErrInvalidLocation
	}

	return Location{
		ID:        id,
		Name:      name,
		CreatedAt: createdAt.UTC(),
	}, nil
}
