package tursocloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const platformAPI = "https://api.turso.tech"

type Config struct {
	BaseURL       string
	Token         string
	Organization  string
	Group         string
	HTTPClient    *http.Client
	PollInterval  time.Duration
	DeleteTimeout time.Duration
}

type Database struct {
	Name         string
	ID           string
	Hostname     string
	URL          string
	AuthToken    string
	Organization string

	client        *client
	deleteTimeout time.Duration
	pollInterval  time.Duration
}

type client struct {
	baseURL      string
	token        string
	organization string
	http         *http.Client
}

func Provision(ctx context.Context, cfg Config) (*Database, error) {
	configured, err := configure(cfg)
	if err != nil {
		return nil, err
	}
	organization, err := configured.organizationSlug(ctx)
	if err != nil {
		return nil, err
	}
	configured.organization = organization
	if err := configured.requireDeletionEnabledGroup(ctx, cfg.Group); err != nil {
		return nil, err
	}

	name, err := disposableName()
	if err != nil {
		return nil, fmt.Errorf("generate disposable Turso database name: %w", err)
	}
	database := &Database{
		Name:          name,
		Organization:  organization,
		client:        configured,
		deleteTimeout: cfg.DeleteTimeout,
		pollInterval:  cfg.PollInterval,
	}
	created, err := configured.createDatabase(ctx, name, cfg.Group)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			database.effectiveDeleteTimeout(),
		)
		defer cancel()
		return nil, errors.Join(err, database.Delete(cleanupCtx))
	}
	database.Name = created.Name
	database.ID = created.ID
	database.Hostname = created.Hostname
	database.URL = "libsql://" + created.Hostname

	token, err := configured.createDatabaseToken(ctx, database.Name)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			database.effectiveDeleteTimeout(),
		)
		defer cancel()
		return nil, errors.Join(err, database.Delete(cleanupCtx))
	}
	database.AuthToken = token
	return database, nil
}

func (d *Database) Delete(ctx context.Context) error {
	if d == nil || d.client == nil || d.Name == "" {
		return fmt.Errorf("disposable Turso database is not initialized")
	}
	deleteCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		deleteCtx, cancel = context.WithTimeout(ctx, d.effectiveDeleteTimeout())
		defer cancel()
	}

	path := d.client.databasePath(d.Name)
	status, err := d.client.request(deleteCtx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return fmt.Errorf("delete disposable Turso database: %w", err)
	}
	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusNotFound {
		return fmt.Errorf("delete disposable Turso database: platform API status %d", status)
	}

	ticker := time.NewTicker(d.effectivePollInterval())
	defer ticker.Stop()
	for {
		status, err := d.client.request(deleteCtx, http.MethodGet, path, nil, nil)
		if err != nil {
			return fmt.Errorf("verify disposable Turso database deletion: %w", err)
		}
		switch status {
		case http.StatusNotFound:
			return nil
		case http.StatusOK:
		default:
			return fmt.Errorf("verify disposable Turso database deletion: platform API status %d", status)
		}
		select {
		case <-deleteCtx.Done():
			return fmt.Errorf("verify disposable Turso database deletion: %w", deleteCtx.Err())
		case <-ticker.C:
		}
	}
}

func configure(cfg Config) (*client, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("Turso Platform API token is required")
	}
	if strings.TrimSpace(cfg.Group) == "" {
		return nil, fmt.Errorf("dedicated Turso CI group is required")
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = platformAPI
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("Turso Platform API base URL is invalid")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &client{
		baseURL:      baseURL,
		token:        cfg.Token,
		organization: strings.TrimSpace(cfg.Organization),
		http:         httpClient,
	}, nil
}

func (c *client) organizationSlug(ctx context.Context) (string, error) {
	if c.organization != "" {
		return c.organization, nil
	}
	var response organizationsResponse
	status, err := c.request(ctx, http.MethodGet, "/v1/organizations", nil, &response)
	if err != nil {
		return "", fmt.Errorf("list Turso organizations: %w", err)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("list Turso organizations: platform API status %d", status)
	}
	if len(response) != 1 || response[0].Slug == "" {
		return "", fmt.Errorf("an explicit organization is required when the Platform API exposes %d organizations", len(response))
	}
	return response[0].Slug, nil
}

type organization struct {
	Slug string `json:"slug"`
}

type organizationsResponse []organization

func (r *organizationsResponse) UnmarshalJSON(data []byte) error {
	var direct []organization
	if err := json.Unmarshal(data, &direct); err == nil {
		*r = direct
		return nil
	}
	var legacy struct {
		Organizations []organization `json:"organizations"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	*r = legacy.Organizations
	return nil
}

func (c *client) requireDeletionEnabledGroup(ctx context.Context, group string) error {
	var response struct {
		Groups []struct {
			Name             string `json:"name"`
			DeleteProtection bool   `json:"delete_protection"`
		} `json:"groups"`
	}
	path := fmt.Sprintf("/v1/organizations/%s/groups", url.PathEscape(c.organization))
	status, err := c.request(ctx, http.MethodGet, path, nil, &response)
	if err != nil {
		return fmt.Errorf("inspect dedicated Turso CI group: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("inspect dedicated Turso CI group: platform API status %d", status)
	}
	for _, candidate := range response.Groups {
		if candidate.Name != group {
			continue
		}
		if candidate.DeleteProtection {
			return fmt.Errorf("dedicated Turso CI group %q has delete protection enabled", group)
		}
		return nil
	}
	return fmt.Errorf("dedicated Turso CI group %q was not found", group)
}

type createdDatabase struct {
	Name     string
	ID       string
	Hostname string
}

func (c *client) createDatabase(ctx context.Context, name, group string) (createdDatabase, error) {
	body := struct {
		Name  string `json:"name"`
		Group string `json:"group"`
	}{Name: name, Group: group}
	var response struct {
		Database struct {
			Name     string `json:"Name"`
			ID       string `json:"DbId"`
			Hostname string `json:"Hostname"`
		} `json:"database"`
	}
	path := fmt.Sprintf("/v1/organizations/%s/databases", url.PathEscape(c.organization))
	status, err := c.request(ctx, http.MethodPost, path, body, &response)
	if err != nil {
		return createdDatabase{}, fmt.Errorf("create disposable Turso database: %w", err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return createdDatabase{}, fmt.Errorf("create disposable Turso database: platform API status %d", status)
	}
	if response.Database.Name != name || response.Database.ID == "" || response.Database.Hostname == "" {
		return createdDatabase{}, fmt.Errorf("create disposable Turso database: platform API returned an invalid database identity")
	}
	return createdDatabase{
		Name: response.Database.Name, ID: response.Database.ID, Hostname: response.Database.Hostname,
	}, nil
}

func (c *client) createDatabaseToken(ctx context.Context, name string) (string, error) {
	path := c.databasePath(name) + "/auth/tokens?expiration=10m&authorization=full-access"
	var response struct {
		JWT string `json:"jwt"`
	}
	status, err := c.request(ctx, http.MethodPost, path, nil, &response)
	if err != nil {
		return "", fmt.Errorf("mint disposable Turso database token: %w", err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return "", fmt.Errorf("mint disposable Turso database token: platform API status %d", status)
	}
	if response.JWT == "" {
		return "", fmt.Errorf("mint disposable Turso database token: platform API returned an empty token")
	}
	return response.JWT, nil
}

func (c *client) databasePath(name string) string {
	return fmt.Sprintf(
		"/v1/organizations/%s/databases/%s",
		url.PathEscape(c.organization),
		url.PathEscape(name),
	)
}

func (c *client) request(ctx context.Context, method, path string, body, target any) (int, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode Platform API request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return 0, fmt.Errorf("build Platform API request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, fmt.Errorf("perform Platform API request: %w", err)
	}
	defer response.Body.Close()
	if target != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target); err != nil {
			return 0, fmt.Errorf("decode Platform API response: %w", err)
		}
	}
	return response.StatusCode, nil
}

func disposableName() (string, error) {
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"xisnove-ci-%d-%s",
		time.Now().UTC().Unix(),
		hex.EncodeToString(random),
	), nil
}

func (d *Database) effectivePollInterval() time.Duration {
	if d.pollInterval > 0 {
		return d.pollInterval
	}
	return time.Second
}

func (d *Database) effectiveDeleteTimeout() time.Duration {
	if d.deleteTimeout > 0 {
		return d.deleteTimeout
	}
	return 30 * time.Second
}
