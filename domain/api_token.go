package domain

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrInvalidAPITokenLabel = errors.New("API token label must contain between 1 and 120 characters")

func NormalizeAPITokenLabel(value string) (string, error) {
	normalized := strings.TrimSpace(value)
	length := utf8.RuneCountInString(normalized)
	if length == 0 || length > 120 {
		return "", ErrInvalidAPITokenLabel
	}
	return normalized, nil
}
