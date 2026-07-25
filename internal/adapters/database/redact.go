package database

import (
	"net/url"
	"strings"
)

type redactedError struct {
	message string
	cause   error
}

func (e redactedError) Error() string { return e.message }
func (e redactedError) Unwrap() error { return e.cause }

func redactDatabaseError(err error, config Config) error {
	if err == nil {
		return nil
	}
	secrets := []string{config.URL, config.AuthToken}
	if parsed, parseErr := url.Parse(config.URL); parseErr == nil {
		if parsed.User != nil {
			secrets = append(secrets, parsed.User.Username())
			if password, ok := parsed.User.Password(); ok {
				secrets = append(secrets, password)
			}
		}
		for key, values := range parsed.Query() {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "password") ||
				strings.Contains(lower, "token") ||
				strings.Contains(lower, "secret") ||
				strings.Contains(lower, "key") {
				secrets = append(secrets, values...)
			}
		}
	}
	message := err.Error()
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "<redacted>")
		}
	}
	if message == err.Error() {
		return err
	}
	return redactedError{message: message, cause: err}
}

var _ interface {
	Error() string
	Unwrap() error
} = redactedError{}
