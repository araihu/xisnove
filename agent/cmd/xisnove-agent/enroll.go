package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/araihu/xisnove/agent/credentials"
	"github.com/araihu/xisnove/agent/internal/controlplane"
)

type enrollUsageError struct{ err error }

func (e *enrollUsageError) Error() string { return e.err.Error() }

type enrollmentJournal struct {
	URL            string                         `json:"url"`
	Name           string                         `json:"name"`
	Capabilities   []controlplane.AgentCapability `json:"capabilities"`
	Credential     string                         `json:"credential"`
	IdempotencyKey string                         `json:"idempotencyKey"`
}

func enrollCommand(parent context.Context, args []string) error {
	flags := flag.NewFlagSet("enroll", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	rawURL := flags.String("url", "", "control-plane URL")
	tokenFile := flags.String("token-file", "", "one-time enrollment token file")
	credentialFile := flags.String("credential-file", "", "durable Agent credential bundle")
	name := flags.String("name", "", "Agent name")
	rawCapabilities := flags.String("capabilities", "", "comma-separated Agent capabilities")
	timeout := flags.Duration("timeout", 30*time.Second, "overall enrollment timeout")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		if err == nil {
			err = errors.New("unexpected positional arguments")
		}
		return &enrollUsageError{err: err}
	}
	parsedURL, err := url.Parse(strings.TrimSpace(*rawURL))
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return &enrollUsageError{err: errors.New("--url must be an absolute HTTP or HTTPS URL")}
	}
	if strings.TrimSpace(*tokenFile) == "" || strings.TrimSpace(*credentialFile) == "" || strings.TrimSpace(*name) == "" || strings.TrimSpace(*rawCapabilities) == "" {
		return &enrollUsageError{err: errors.New("--token-file, --credential-file, --name, and --capabilities are required")}
	}
	if *timeout <= 0 {
		return &enrollUsageError{err: errors.New("--timeout must be positive")}
	}
	capabilities, err := parseCapabilities(*rawCapabilities)
	if err != nil {
		return &enrollUsageError{err: err}
	}
	if _, err := (credentials.FileProvider{Path: *credentialFile}).Current(parent); err == nil {
		return nil
	} else if !errors.Is(sanitizedPathCause(err), os.ErrNotExist) {
		return errors.New("credential file exists but is unavailable or invalid")
	}

	journalPath := *credentialFile + ".enroll-state"
	journal, err := loadOrCreateEnrollmentJournal(journalPath, enrollmentJournal{
		URL: strings.TrimRight(parsedURL.String(), "/"), Name: strings.TrimSpace(*name),
		Capabilities: capabilities,
	})
	if err != nil {
		return err
	}
	if journal.URL != strings.TrimRight(parsedURL.String(), "/") || journal.Name != strings.TrimSpace(*name) || !sameCapabilities(journal.Capabilities, capabilities) {
		return errors.New("enrollment state does not match requested Agent identity")
	}
	token, err := readWorkloadSecret(*tokenFile, 64<<10)
	if err != nil {
		return errors.New("enrollment token file is unavailable or unsafe")
	}
	defer clear(token)
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	client, err := controlplane.NewClientWithResponses(journal.URL)
	if err != nil {
		return errors.New("create enrollment client")
	}
	response, err := client.EnrollAgentWithResponse(ctx,
		&controlplane.EnrollAgentParams{IdempotencyKey: controlplane.RequiredIdempotencyKey(journal.IdempotencyKey)},
		controlplane.EnrollAgentJSONRequestBody{
			Token: stringPointer(string(token)), Credential: stringPointer(journal.Credential),
			Name: journal.Name, Capabilities: journal.Capabilities,
		},
	)
	if err != nil {
		return errors.New("Agent enrollment request failed")
	}
	if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
		return fmt.Errorf("Agent enrollment failed with HTTP %d", response.StatusCode())
	}
	if response.JSON201.Credential != journal.Credential || response.JSON201.CredentialGeneration <= 0 {
		return errors.New("Agent enrollment returned an invalid credential")
	}
	bundle, err := json.Marshal(credentials.Bundle{Credential: journal.Credential, Generation: response.JSON201.CredentialGeneration})
	if err != nil {
		return errors.New("encode Agent credential bundle")
	}
	if err := writeAtomicNoReplace(*credentialFile, append(bundle, '\n'), 0o600); err != nil {
		if errors.Is(err, os.ErrExist) {
			if _, readErr := (credentials.FileProvider{Path: *credentialFile}).Current(parent); readErr == nil {
				_ = os.Remove(journalPath)
				return nil
			}
		}
		return errors.New("persist Agent credential bundle")
	}
	_ = os.Remove(journalPath)
	return nil
}

func loadOrCreateEnrollmentJournal(path string, template enrollmentJournal) (enrollmentJournal, error) {
	contents, err := readWorkloadSecret(path, 64<<10)
	if err == nil {
		var journal enrollmentJournal
		if json.Unmarshal(contents, &journal) != nil || journal.Credential == "" || journal.IdempotencyKey == "" {
			return enrollmentJournal{}, errors.New("enrollment state is invalid")
		}
		return journal, nil
	}
	if !errors.Is(sanitizedPathCause(err), os.ErrNotExist) {
		return enrollmentJournal{}, errors.New("enrollment state is unavailable or unsafe")
	}
	credential, err := randomURLSecret(32)
	if err != nil {
		return enrollmentJournal{}, errors.New("generate Agent credential")
	}
	idempotency, err := randomURLSecret(18)
	if err != nil {
		return enrollmentJournal{}, errors.New("generate enrollment idempotency key")
	}
	template.Credential = credential
	template.IdempotencyKey = "agent-enroll-" + idempotency
	contents, err = json.Marshal(template)
	if err != nil {
		return enrollmentJournal{}, errors.New("encode enrollment state")
	}
	if err := writeAtomicNoReplace(path, append(contents, '\n'), 0o600); err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadOrCreateEnrollmentJournal(path, template)
		}
		return enrollmentJournal{}, errors.New("persist enrollment state")
	}
	return template, nil
}

func readWorkloadSecret(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o037 != 0 || info.Mode().Perm()&0o440 == 0 {
		return nil, errors.New("file permissions exceed workload read access")
	}
	contents, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > limit {
		return nil, errors.New("file exceeds size limit")
	}
	contents = []byte(strings.TrimSpace(string(contents)))
	if len(contents) == 0 {
		return nil, errors.New("file is empty")
	}
	return contents, nil
}

func writeAtomicNoReplace(path string, contents []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".xisnove-secret-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err == nil {
		err = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return err
}

func randomURLSecret(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func sanitizedPathCause(err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return pathError.Err
	}
	return err
}

func sameCapabilities(left, right []controlplane.AgentCapability) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func stringPointer(value string) *string { return &value }
