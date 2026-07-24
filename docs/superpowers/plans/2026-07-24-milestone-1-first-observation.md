# Milestone 1 First Observation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the first complete Xisnove observation path: bootstrap an administrator, configure a Location and HTTP Monitor through the public API, enroll an outbound agent, lease and execute scheduled HTTP work, ingest an idempotent result, derive health, and open an Incident in SQLite.

**Architecture:** The root Go module contains the OpenAPI contract, generated strict `net/http` server, public SDK, domain/application code, and SQLite adapter. A separate `agent/` Go module generates its own internal client from the same OpenAPI document, keeping the agent independent from server packages. Hexagonal boundaries keep generated API and sqlc types in adapters, while application services control transactions through explicit repository ports.

**Tech Stack:** Go 1.26.1, OpenAPI 3.1.2, `oapi-codegen` v2.8.0, `kin-openapi` v0.144.0, `nethttp-middleware` v1.2.0, sqlc v1.31.1, goose v3.27.3, `modernc.org/sqlite` v1.54.0, `golang.org/x/crypto` v0.54.0, `google/uuid` v1.6.0, Vacuum v0.30.0, oasdiff v1.26.0.

## Global Constraints

- The canonical contract is `api/openapi.yaml` using OpenAPI 3.1.2.
- Pin `oapi-codegen` to v2.8.0, the newest stable release verified on 2026-07-24; do not use `HEAD` or an unreviewed prerelease.
- Generate and commit the strict server, public SDK, and agent-internal client; generated-file drift fails CI.
- Use `net/http` from Go 1.26.1; do not add a third-party router.
- Use SQLite through `database/sql` and `modernc.org/sqlite` with one open connection, WAL mode, foreign keys, and a 5-second busy timeout.
- Use sqlc for all relational queries; handwritten SQL is limited to migrations, sqlc query files, and connection PRAGMAs.
- UI, CLI, Turso, PostgreSQL, notification delivery, Kubernetes operator/discovery, TCP, DNS, TLS-expiry evaluation, maintenance, and public status pages are outside this milestone.
- Agents are outbound-only, receive no database credentials, and do not import root-module server, domain, application, or persistence packages.
- Domain packages do not import OpenAPI, sqlc, SQL driver, HTTP adapter, or agent packages.
- Use fixed schedules, defaulting to three consecutive failures and two consecutive successes.
- Every task follows red-green-refactor, ends with the listed verification, and creates the listed focused commit.

---

## Milestone boundary

The milestone is complete only when one integration test proves this sequence:

1. migrate a fresh SQLite database;
2. bootstrap the only local administrator;
3. create an administrator session through the API;
4. create a Location and an HTTP Monitor assigned to it;
5. create and consume a one-time Agent enrollment token;
6. schedule and lease three failing HTTP checks;
7. acknowledge three idempotent result uploads;
8. observe Location and Monitor health transition to `down`;
9. observe one active critical Incident with one opening event;
10. upload a duplicate result and prove that it creates no second transition.

## File and package map

| Path | Responsibility |
|---|---|
| `api/openapi.yaml` | Canonical management and Agent protocol contract |
| `api/oapi-codegen-server.yaml` | Strict standard-library server generation |
| `api/oapi-codegen-sdk.yaml` | Public Go SDK generation |
| `internal/domain/` | IDs, Monitor invariants, health projection, Incident decisions |
| `internal/application/` | Use cases and transaction/repository ports |
| `internal/adapters/httpapi/` | Generated server, request mapping, auth middleware, RFC 9457 responses |
| `internal/adapters/sqlite/` | SQLite connection, transactions, repository mapping |
| `internal/adapters/crypto/` | Password hashing and opaque token generation |
| `db/migrations/sqlite/` | Embedded goose migrations |
| `db/queries/sqlite/` | sqlc queries grouped by responsibility |
| `db/generated/sqlite/` | Committed sqlc output |
| `sdk/` | Committed generated public Go client and helpers |
| `cmd/xisnove-server/` | `serve`, `db migrate`, and `admin bootstrap` composition |
| `agent/` | Independent Agent module, generated client, HTTP probe, and worker loop |
| `integration/` | Root-module first-observation test through HTTP boundaries |

---

### Task 1: Reproducible Go workspace and configuration

**Files:**
- Create: `go.mod`
- Create: `go.work`
- Create: `agent/go.mod`
- Create: `Makefile`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Load(getenv func(string) string) (config.Config, error)`
- Produces: `config.Config{ListenAddr, DatabasePath, LeaseDuration, SessionDuration}`
- Consumes: no application packages

- [ ] **Step 1: Write the failing configuration test**

```go
package config_test

import (
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/config"
)

func TestLoadUsesSafeDefaults(t *testing.T) {
	cfg, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.DatabasePath != "xisnove.db" {
		t.Fatalf("DatabasePath = %q", cfg.DatabasePath)
	}
	if cfg.LeaseDuration != 30*time.Second {
		t.Fatalf("LeaseDuration = %s", cfg.LeaseDuration)
	}
	if cfg.SessionDuration != 12*time.Hour {
		t.Fatalf("SessionDuration = %s", cfg.SessionDuration)
	}
}

func TestLoadRejectsNonPositiveLeaseDuration(t *testing.T) {
	_, err := config.Load(func(key string) string {
		if key == "XISNOVE_LEASE_DURATION" {
			return "0s"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected invalid lease duration")
	}
}
```

- [ ] **Step 2: Initialize the modules and verify the test fails**

Run:

```bash
go mod init github.com/araihu/xisnove
go mod edit -go=1.26.0 -toolchain=go1.26.1
mkdir -p agent
cd agent
go mod init github.com/araihu/xisnove/agent
go mod edit -go=1.26.0 -toolchain=go1.26.1
cd ..
go work init . ./agent
go test ./internal/config
```

Expected: FAIL because `internal/config` does not exist.

- [ ] **Step 3: Implement the minimal configuration parser**

```go
package config

import (
	"fmt"
	"time"
)

type Config struct {
	ListenAddr     string
	DatabasePath   string
	LeaseDuration time.Duration
	SessionDuration time.Duration
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		ListenAddr:      valueOr(getenv("XISNOVE_LISTEN_ADDR"), "127.0.0.1:8080"),
		DatabasePath:    valueOr(getenv("XISNOVE_DATABASE_PATH"), "xisnove.db"),
		LeaseDuration:   30 * time.Second,
		SessionDuration: 12 * time.Hour,
	}
	var err error
	if raw := getenv("XISNOVE_LEASE_DURATION"); raw != "" {
		cfg.LeaseDuration, err = time.ParseDuration(raw)
		if err != nil || cfg.LeaseDuration <= 0 {
			return Config{}, fmt.Errorf("XISNOVE_LEASE_DURATION must be positive: %q", raw)
		}
	}
	if raw := getenv("XISNOVE_SESSION_DURATION"); raw != "" {
		cfg.SessionDuration, err = time.ParseDuration(raw)
		if err != nil || cfg.SessionDuration <= 0 {
			return Config{}, fmt.Errorf("XISNOVE_SESSION_DURATION must be positive: %q", raw)
		}
	}
	return cfg, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
```

Create a `Makefile` with these stable entry points:

```make
.PHONY: generate test check

generate:
	go generate ./...
	cd agent && go generate ./...

test:
	go test ./...
	cd agent && GOWORK=off go test ./...

check: generate
	git diff --exit-code
	go vet ./...
	go test -race ./...
	cd agent && GOWORK=off go vet ./...
	cd agent && GOWORK=off go test -race ./...
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/config`

Expected: PASS.

- [ ] **Step 5: Commit the workspace foundation**

```bash
git add go.mod go.work agent/go.mod Makefile internal/config
git commit -m "build: initialize Go workspace"
```

---

### Task 2: Canonical OpenAPI contract and generated boundaries

**Files:**
- Create: `api/openapi.yaml`
- Create: `api/contract_test.go`
- Create: `api/oapi-codegen-server.yaml`
- Create: `api/oapi-codegen-sdk.yaml`
- Create: `internal/adapters/httpapi/generate.go`
- Create: `internal/adapters/httpapi/generated.gen.go`
- Create: `sdk/generate.go`
- Create: `sdk/generated.gen.go`
- Create: `sdk/helpers.go`
- Test: `sdk/helpers_test.go`
- Modify: `go.mod`

**Interfaces:**
- Produces: generated `httpapi.StrictServerInterface`
- Produces: generated `sdk.ClientWithResponses`
- Produces operation IDs: `createSession`, `createLocation`, `createMonitor`, `getMonitor`, `createAgentEnrollmentToken`, `enrollAgent`, `heartbeatAgent`, `leaseAgentWork`, `uploadProbeResults`, `getMonitorHealth`, `getActiveMonitorIncident`
- Consumes: no domain types

- [ ] **Step 1: Write a failing contract test**

```go
package api_test

import (
	"context"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestContractIsOpenAPI312AndValid(t *testing.T) {
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.2" {
		t.Fatalf("OpenAPI = %q", doc.OpenAPI)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"createSession": false, "createLocation": false, "createMonitor": false,
		"getMonitor": false, "createAgentEnrollmentToken": false,
		"enrollAgent": false, "heartbeatAgent": false, "leaseAgentWork": false,
		"uploadProbeResults": false, "getMonitorHealth": false,
		"getActiveMonitorIncident": false,
	}
	for _, item := range doc.Paths.Map() {
		for _, op := range []*openapi3.Operation{
			item.Get, item.Post, item.Put, item.Patch, item.Delete,
		} {
			if op != nil {
				if _, ok := want[op.OperationID]; ok {
					want[op.OperationID] = true
				}
			}
		}
	}
	for operationID, found := range want {
		if !found {
			t.Errorf("missing operationId %s", operationID)
		}
	}
}
```

- [ ] **Step 2: Pin the generators and verify the test fails**

Run:

```bash
go get github.com/getkin/kin-openapi@v0.144.0
go get github.com/oapi-codegen/nethttp-middleware@v1.2.0
go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0
go get -tool github.com/daveshanley/vacuum@v0.30.0
go get -tool github.com/tufin/oasdiff@v1.26.0
go test ./api
```

Expected: FAIL with `openapi.yaml: no such file or directory`.

- [ ] **Step 3: Write the milestone contract**

Create `api/openapi.yaml` with:

```yaml
openapi: 3.1.2
info:
  title: Xisnove API
  version: 0.1.0
jsonSchemaDialect: https://json-schema.org/draft/2020-12/schema
servers:
  - url: /
tags:
  - name: auth
  - name: management
  - name: agent
paths:
  /v1/sessions:
    post:
      operationId: createSession
      tags: [auth]
      security: []
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/CreateSessionRequest'}
      responses:
        '201':
          description: Session created
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Session'}
        default: {$ref: '#/components/responses/Problem'}
  /v1/locations:
    post:
      operationId: createLocation
      tags: [management]
      security: [{adminBearer: []}]
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/CreateLocationRequest'}
      responses:
        '201':
          description: Location created
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Location'}
        default: {$ref: '#/components/responses/Problem'}
  /v1/monitors:
    post:
      operationId: createMonitor
      tags: [management]
      security: [{adminBearer: []}]
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/CreateMonitorRequest'}
      responses:
        '201':
          description: Monitor created
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Monitor'}
        default: {$ref: '#/components/responses/Problem'}
  /v1/monitors/{monitorId}:
    parameters:
      - {$ref: '#/components/parameters/MonitorID'}
    get:
      operationId: getMonitor
      tags: [management]
      security: [{adminBearer: []}]
      responses:
        '200':
          description: Monitor
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Monitor'}
        default: {$ref: '#/components/responses/Problem'}
  /v1/agent-enrollment-tokens:
    post:
      operationId: createAgentEnrollmentToken
      tags: [management]
      security: [{adminBearer: []}]
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/CreateAgentEnrollmentTokenRequest'}
      responses:
        '201':
          description: One-time token
          content:
            application/json:
              schema: {$ref: '#/components/schemas/AgentEnrollmentToken'}
        default: {$ref: '#/components/responses/Problem'}
  /v1/agent-enrollments:
    post:
      operationId: enrollAgent
      tags: [agent]
      security: []
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/EnrollAgentRequest'}
      responses:
        '201':
          description: Agent credential
          content:
            application/json:
              schema: {$ref: '#/components/schemas/EnrolledAgent'}
        default: {$ref: '#/components/responses/Problem'}
  /v1/agent/heartbeat:
    post:
      operationId: heartbeatAgent
      tags: [agent]
      security: [{agentBearer: []}]
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/AgentHeartbeat'}
      responses:
        '204': {description: Heartbeat accepted}
        default: {$ref: '#/components/responses/Problem'}
  /v1/agent/work:lease:
    post:
      operationId: leaseAgentWork
      tags: [agent]
      security: [{agentBearer: []}]
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/LeaseWorkRequest'}
      responses:
        '200':
          description: Leased HTTP work
          content:
            application/json:
              schema: {$ref: '#/components/schemas/HTTPWork'}
        '204': {description: No compatible work before timeout}
        default: {$ref: '#/components/responses/Problem'}
  /v1/agent/results:batch:
    post:
      operationId: uploadProbeResults
      tags: [agent]
      security: [{agentBearer: []}]
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [results]
              properties:
                results:
                  type: array
                  minItems: 1
                  maxItems: 100
                  items: {$ref: '#/components/schemas/ProbeResultInput'}
      responses:
        '200':
          description: Per-result acknowledgements
          content:
            application/json:
              schema:
                type: object
                required: [acknowledgements]
                properties:
                  acknowledgements:
                    type: array
                    items: {$ref: '#/components/schemas/ProbeResultAcknowledgement'}
        default: {$ref: '#/components/responses/Problem'}
  /v1/monitors/{monitorId}/health:
    parameters:
      - {$ref: '#/components/parameters/MonitorID'}
    get:
      operationId: getMonitorHealth
      tags: [management]
      security: [{adminBearer: []}]
      responses:
        '200':
          description: Monitor health
          content:
            application/json:
              schema: {$ref: '#/components/schemas/MonitorHealth'}
        default: {$ref: '#/components/responses/Problem'}
  /v1/monitors/{monitorId}/active-incident:
    parameters:
      - {$ref: '#/components/parameters/MonitorID'}
    get:
      operationId: getActiveMonitorIncident
      tags: [management]
      security: [{adminBearer: []}]
      responses:
        '200':
          description: Active Incident
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Incident'}
        '204': {description: No active Incident}
        default: {$ref: '#/components/responses/Problem'}
components:
  securitySchemes:
    adminBearer: {type: http, scheme: bearer, bearerFormat: opaque}
    agentBearer: {type: http, scheme: bearer, bearerFormat: opaque}
  parameters:
    MonitorID:
      name: monitorId
      in: path
      required: true
      schema: {type: string, format: uuid}
  responses:
    Problem:
      description: Problem details
      content:
        application/problem+json:
          schema: {$ref: '#/components/schemas/Problem'}
```

Define the referenced schemas with exact required fields:

| Schema | Required fields |
|---|---|
| `CreateSessionRequest` | `email`, `password` |
| `Session` | `token`, `expiresAt` |
| `CreateLocationRequest` | `name` |
| `Location` | `id`, `name`, `createdAt` |
| `CreateMonitorRequest` | `name`, `intervalSeconds`, `timeoutMillis`, `failureThreshold`, `recoveryThreshold`, `locationId`, `requiredLocation`, `http` |
| `HTTPProbe` | `method`, `url`, `expectedStatus`, `bodyContains`, `followRedirects` |
| `Monitor` | request fields plus `id`, `kind: http`, `createdAt`, `updatedAt` |
| `CreateAgentEnrollmentTokenRequest` | `locationId`, `expiresInSeconds` |
| `AgentEnrollmentToken` | `token`, `expiresAt` |
| `EnrollAgentRequest` | `token`, `name`, `capabilities` |
| `EnrolledAgent` | `agentId`, `credential`, `credentialGeneration` |
| `AgentHeartbeat` | `version`, `credentialGeneration`, `capabilities` |
| `LeaseWorkRequest` | `waitSeconds`, `capabilities` |
| `HTTPWork` | `runId`, `leaseToken`, `monitorId`, `scheduledFor`, `timeoutMillis`, `http` |
| `ProbeResultInput` | `resultId`, `runId`, `leaseToken`, `startedAt`, `finishedAt`, `outcome`, `latencyMillis`, `observedStatus`, `bodyAssertionPassed`, `errorCode`, `diagnosticSample` |
| `ProbeResultAcknowledgement` | `resultId`, `status` where status is `accepted` or `duplicate` |
| `MonitorHealth` | `monitorId`, `state`, `lastTransitionAt`, `locations` |
| `Incident` | `id`, `monitorId`, `state`, `severity`, `openedAt`, `lastTransitionAt` |
| `Problem` | `type`, `title`, `status`, `code`, `correlationId`, optional `detail` and `fieldErrors` |

Use UUID strings, RFC 3339 `date-time`, closed string enums, integer ranges,
`additionalProperties: false`, and `writeOnly: true` for passwords and raw
tokens.

- [ ] **Step 4: Configure and run generation**

Server configuration:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/oapi-codegen/oapi-codegen/v2.8.0/configuration-schema.json
package: httpapi
generate:
  models: true
  std-http-server: true
  strict-server: true
  embedded-spec: true
output: generated.gen.go
```

SDK configuration:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/oapi-codegen/oapi-codegen/v2.8.0/configuration-schema.json
package: sdk
generate:
  models: true
  client: true
output: generated.gen.go
```

Generation directives:

```go
// internal/adapters/httpapi/generate.go
package httpapi

//go:generate go tool oapi-codegen -config ../../../api/oapi-codegen-server.yaml ../../../api/openapi.yaml
```

```go
// sdk/generate.go
package sdk

//go:generate go tool oapi-codegen -config ../api/oapi-codegen-sdk.yaml ../api/openapi.yaml
```

Run:

```bash
go generate ./internal/adapters/httpapi ./sdk
go tool vacuum lint -d api/openapi.yaml
go test ./api ./sdk
```

Expected: generation succeeds, lint reports no errors, tests PASS.

- [ ] **Step 5: Add and test one ergonomic SDK helper**

```go
package sdk

import (
	"context"
	"fmt"
)

func (c *ClientWithResponses) RequireMonitor(
	ctx context.Context,
	monitorID string,
	reqEditors ...RequestEditorFn,
) (*Monitor, error) {
	response, err := c.GetMonitorWithResponse(ctx, monitorID, reqEditors...)
	if err != nil {
		return nil, err
	}
	if response.JSON200 == nil {
		return nil, fmt.Errorf("get monitor: HTTP %d", response.StatusCode())
	}
	return response.JSON200, nil
}
```

Test with an `httptest.Server` that returns one valid Monitor and assert the
helper returns it; add a second case returning RFC 9457 JSON and assert the
helper includes the HTTP status.

- [ ] **Step 6: Commit the contract**

```bash
git add go.mod go.sum api internal/adapters/httpapi sdk
git commit -m "feat(api): define first observation contract"
```

---

### Task 3: Monitor and Location domain model

**Files:**
- Create: `internal/domain/id.go`
- Create: `internal/domain/location.go`
- Create: `internal/domain/monitor.go`
- Test: `internal/domain/monitor_test.go`

**Interfaces:**
- Produces: `domain.NewLocation(domain.LocationID, string, time.Time) (domain.Location, error)`
- Produces: `domain.NewHTTPMonitor(domain.NewHTTPMonitorParams) (domain.Monitor, error)`
- Produces ID aliases: `MonitorID`, `LocationID`, `AgentID`, `CheckRunID`, `IncidentID`
- Consumes: only Go standard library

- [ ] **Step 1: Write failing invariant tests**

```go
func TestNewHTTPMonitorAppliesAndValidatesInvariants(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID: domain.MonitorID("m1"), Name: "router",
		Interval: 60 * time.Second, Timeout: 5 * time.Second,
		FailureThreshold: 3, RecoveryThreshold: 2,
		HTTP: domain.HTTPProbe{
			Method: "GET", URL: "https://router.example/health",
			ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}},
			BodyContains: []string{"ok"},
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Kind != domain.MonitorKindHTTP {
		t.Fatalf("Kind = %q", monitor.Kind)
	}
}

func TestNewHTTPMonitorRejectsTimeoutAtOrAboveInterval(t *testing.T) {
	_, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID: "m1", Name: "bad", Interval: time.Second, Timeout: time.Second,
		FailureThreshold: 3, RecoveryThreshold: 2,
		HTTP: domain.HTTPProbe{Method: "GET", URL: "https://example.com"},
		CreatedAt: time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidMonitor) {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/domain -run 'TestNewHTTPMonitor'`

Expected: FAIL because the domain types do not exist.

- [ ] **Step 3: Implement focused domain files**

Define typed IDs in `id.go`:

```go
type MonitorID string
type LocationID string
type AgentID string
type CheckRunID string
type IncidentID string
```

Define Monitor construction:

```go
var ErrInvalidMonitor = errors.New("invalid monitor")

type MonitorKind string
const MonitorKindHTTP MonitorKind = "http"

type StatusRange struct { Min, Max int }

type HTTPProbe struct {
	Method          string
	URL             string
	ExpectedStatus  []StatusRange
	BodyContains    []string
	FollowRedirects bool
}

type NewHTTPMonitorParams struct {
	ID MonitorID
	Name string
	Interval time.Duration
	Timeout time.Duration
	FailureThreshold uint16
	RecoveryThreshold uint16
	HTTP HTTPProbe
	CreatedAt time.Time
}

type Monitor struct {
	ID MonitorID
	Name string
	Kind MonitorKind
	Interval time.Duration
	Timeout time.Duration
	FailureThreshold uint16
	RecoveryThreshold uint16
	HTTP HTTPProbe
	Enabled bool
	NextRunAt time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewHTTPMonitor(p NewHTTPMonitorParams) (Monitor, error) {
	parsed, err := url.ParseRequestURI(p.HTTP.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || strings.TrimSpace(p.Name) == "" ||
		p.Interval <= 0 || p.Timeout <= 0 || p.Timeout >= p.Interval ||
		p.FailureThreshold == 0 || p.RecoveryThreshold == 0 {
		return Monitor{}, ErrInvalidMonitor
	}
	if p.HTTP.Method == "" {
		p.HTTP.Method = http.MethodGet
	}
	for _, status := range p.HTTP.ExpectedStatus {
		if status.Min < 100 || status.Max > 599 || status.Min > status.Max {
			return Monitor{}, ErrInvalidMonitor
		}
	}
	return Monitor{
		ID: p.ID, Name: strings.TrimSpace(p.Name), Kind: MonitorKindHTTP,
		Interval: p.Interval, Timeout: p.Timeout,
		FailureThreshold: p.FailureThreshold,
		RecoveryThreshold: p.RecoveryThreshold,
		HTTP: p.HTTP, Enabled: true, NextRunAt: p.CreatedAt.UTC(),
		CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.CreatedAt.UTC(),
	}, nil
}
```

`Location` is:

```go
type Location struct {
	ID LocationID
	Name string
	CreatedAt time.Time
}
```

`NewLocation` rejects empty IDs/names, trims the name, and stores UTC
`CreatedAt`.

- [ ] **Step 4: Run all domain tests**

Run: `go test ./internal/domain`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain
git commit -m "feat(domain): model HTTP monitors and locations"
```

---

### Task 4: Health projection and Incident decisions

**Files:**
- Create: `internal/domain/health.go`
- Create: `internal/domain/incident.go`
- Test: `internal/domain/health_test.go`
- Test: `internal/domain/incident_test.go`

**Interfaces:**
- Produces: `domain.ApplyProbe(domain.LocationHealth, domain.ProbeObservation, domain.Thresholds) domain.LocationHealth`
- Produces: `domain.RollupRequired([]domain.LocationHealth) domain.HealthState`
- Produces: `domain.DecideIncident(*domain.Incident, domain.MonitorID, domain.HealthState, time.Time, func() domain.IncidentID) domain.IncidentDecision`
- Consumes: ID types from Task 3

- [ ] **Step 1: Write the failing threshold and rollup tests**

```go
func TestApplyProbeUsesFailureAndRecoveryThresholds(t *testing.T) {
	at := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	health := domain.LocationHealth{State: domain.HealthPending}
	for i := 0; i < 2; i++ {
		health = domain.ApplyProbe(health, domain.ProbeObservation{Passed: false, At: at.Add(time.Duration(i) * time.Minute)}, domain.Thresholds{Failures: 3, Recoveries: 2})
		if health.State != domain.HealthPending {
			t.Fatalf("failure %d state = %s", i+1, health.State)
		}
	}
	health = domain.ApplyProbe(health, domain.ProbeObservation{Passed: false, At: at.Add(2 * time.Minute)}, domain.Thresholds{Failures: 3, Recoveries: 2})
	if health.State != domain.HealthDown {
		t.Fatalf("state = %s", health.State)
	}
	health = domain.ApplyProbe(health, domain.ProbeObservation{Passed: true, At: at.Add(3 * time.Minute)}, domain.Thresholds{Failures: 3, Recoveries: 2})
	if health.State != domain.HealthDown {
		t.Fatalf("recovered too early: %s", health.State)
	}
	health = domain.ApplyProbe(health, domain.ProbeObservation{Passed: true, At: at.Add(4 * time.Minute)}, domain.Thresholds{Failures: 3, Recoveries: 2})
	if health.State != domain.HealthUp {
		t.Fatalf("state = %s", health.State)
	}
}

func TestRollupRequiredTruthTable(t *testing.T) {
	tests := []struct {
		name string
		states []domain.HealthState
		want domain.HealthState
	}{
		{"missing", nil, domain.HealthUnknown},
		{"unknown wins", []domain.HealthState{domain.HealthUp, domain.HealthUnknown}, domain.HealthUnknown},
		{"all up", []domain.HealthState{domain.HealthUp, domain.HealthUp}, domain.HealthUp},
		{"all down", []domain.HealthState{domain.HealthDown, domain.HealthDown}, domain.HealthDown},
		{"mixed", []domain.HealthState{domain.HealthUp, domain.HealthDown}, domain.HealthDegraded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := make([]domain.LocationHealth, len(tt.states))
			for i, state := range tt.states { health[i].State = state }
			if got := domain.RollupRequired(health); got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/domain -run 'TestApplyProbe|TestRollup|TestDecideIncident'`

Expected: FAIL because projection functions do not exist.

- [ ] **Step 3: Implement the state machine**

```go
type HealthState string

const (
	HealthPending HealthState = "pending"
	HealthUp HealthState = "up"
	HealthDown HealthState = "down"
	HealthDegraded HealthState = "degraded"
	HealthUnknown HealthState = "unknown"
)

type Thresholds struct { Failures, Recoveries uint16 }
type ProbeObservation struct { Passed bool; At time.Time }

type LocationHealth struct {
	MonitorID MonitorID
	LocationID LocationID
	State HealthState
	ConsecutiveFailures uint16
	ConsecutiveSuccesses uint16
	LastObservedAt time.Time
	LastTransitionAt time.Time
}

type MonitorHealth struct {
	MonitorID MonitorID
	State HealthState
	LastTransitionAt time.Time
}
```

`ApplyProbe` increments exactly one counter, clears the other, transitions to
`down` at the failure threshold and to `up` at the recovery threshold, and
updates `LastTransitionAt` only when state changes. `RollupRequired` applies the
approved unknown/up/down/degraded order and treats `pending` as `unknown` after
the application layer has applied its grace rule.

Incident decisions:

```go
type IncidentAction string
const (
	IncidentNone IncidentAction = "none"
	IncidentOpen IncidentAction = "open"
	IncidentChange IncidentAction = "change"
	IncidentRecover IncidentAction = "recover"
)

type IncidentDecision struct {
	Action IncidentAction
	Incident Incident
	PreviousState HealthState
}

type IncidentSeverity string
const (
	IncidentWarning IncidentSeverity = "warning"
	IncidentCritical IncidentSeverity = "critical"
)

type Incident struct {
	ID IncidentID
	MonitorID MonitorID
	State HealthState
	Severity IncidentSeverity
	OpenedAt time.Time
	LastTransitionAt time.Time
	RecoveredAt *time.Time
}

type IncidentEvent struct {
	ID string
	IncidentID IncidentID
	PreviousState HealthState
	State HealthState
	Severity IncidentSeverity
	CreatedAt time.Time
}
```

`DecideIncident` opens only for `down`, `degraded`, or `unknown`; maps `down` to
critical and the other unhealthy states to warning; changes an existing
Incident only when state or severity changes; and recovers only on `up`.

- [ ] **Step 4: Run the state-machine tests**

Run: `go test ./internal/domain`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain
git commit -m "feat(domain): derive health and incident transitions"
```

---

### Task 5: SQLite schema, sqlc output, and transaction boundary

**Files:**
- Create: `sqlc.yaml`
- Create: `db/migrations/sqlite/00001_initial.sql`
- Create: `db/migrations/sqlite/migrations.go`
- Create: `db/queries/sqlite/auth.sql`
- Create: `db/queries/sqlite/configuration.sql`
- Create: `db/queries/sqlite/agents.sql`
- Create: `db/queries/sqlite/runs.sql`
- Create: `db/queries/sqlite/results.sql`
- Create: `db/queries/sqlite/health.sql`
- Create: `db/queries/sqlite/incidents.sql`
- Create: `db/generated/sqlite/`
- Create: `internal/application/store.go`
- Create: `internal/adapters/sqlite/database.go`
- Create: `internal/adapters/sqlite/migrate.go`
- Create: `internal/adapters/sqlite/store.go`
- Test: `internal/adapters/sqlite/migrate_test.go`
- Test: `internal/adapters/sqlite/store_test.go`
- Modify: `go.mod`

**Interfaces:**
- Produces: `sqlite.Open(path string) (*sql.DB, error)`
- Produces: `sqlite.Migrate(ctx context.Context, db *sql.DB) error`
- Produces: `sqlite.NewStore(db *sql.DB) application.Store`
- Produces: `application.Store.Repositories() application.Repositories`
- Produces: `application.Store.WithinTx(context.Context, func(application.Repositories) error) error`
- Consumes: domain entities from Tasks 3 and 4

- [ ] **Step 1: Define the repository ports and write failing migration tests**

```go
// internal/application/store.go
type Store interface {
	Repositories() Repositories
	WithinTx(context.Context, func(Repositories) error) error
}

type Repositories struct {
	Admins AdminRepository
	Sessions SessionRepository
	Locations LocationRepository
	Monitors MonitorRepository
	Health HealthRepository
}
```

Start with the repositories needed by Tasks 6 and 7. Later tasks extend
`Repositories` only when their domain types exist:

```go
type AdminRecord struct {
	ID, Email, PasswordHash string
	CreatedAt time.Time
}
type SessionRecord struct {
	ID, AdminID string
	TokenHash []byte
	ExpiresAt time.Time
	RevokedAt *time.Time
}
type MonitorLocation struct {
	MonitorID domain.MonitorID
	LocationID domain.LocationID
	Required bool
}

type AdminRepository interface {
	Count(context.Context) (int64, error)
	Create(context.Context, AdminRecord) error
	FindByEmail(context.Context, string) (AdminRecord, error)
}
type SessionRepository interface {
	Create(context.Context, SessionRecord) error
	FindActiveByTokenHash(context.Context, []byte, time.Time) (SessionRecord, error)
}
type LocationRepository interface {
	Create(context.Context, domain.Location) error
	Get(context.Context, domain.LocationID) (domain.Location, error)
}
type MonitorRepository interface {
	Create(context.Context, domain.Monitor) error
	Get(context.Context, domain.MonitorID) (domain.Monitor, error)
	AssignLocation(context.Context, MonitorLocation) error
}
type HealthRepository interface {
	GetLocation(context.Context, domain.MonitorID, domain.LocationID) (domain.LocationHealth, error)
	UpsertLocation(context.Context, domain.LocationHealth) error
	ListRequiredLocations(context.Context, domain.MonitorID) ([]domain.LocationHealth, error)
	GetMonitor(context.Context, domain.MonitorID) (domain.MonitorHealth, error)
	UpsertMonitor(context.Context, domain.MonitorHealth) error
}
```

The initial `Repositories` struct contains `Admins`, `Sessions`, `Locations`,
`Monitors`, and `Health`. Repository methods return
`application.ErrNotFound` for an absent record and never expose generated sqlc
types.

Migration test:

```go
func TestMigrateFreshDatabaseIsIdempotent(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err := sqlite.Migrate(ctx, db); err != nil { t.Fatal(err) }
	if err := sqlite.Migrate(ctx, db); err != nil { t.Fatal(err) }
	var version int
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id), 0) FROM schema_migrations WHERE is_applied = 1").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 { t.Fatalf("migration version = %d", version) }
}
```

- [ ] **Step 2: Pin persistence tools and verify failure**

Run:

```bash
go get modernc.org/sqlite@v1.54.0
go get github.com/pressly/goose/v3@v3.27.3
go get github.com/google/uuid@v1.6.0
go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
go test ./internal/adapters/sqlite
```

Expected: FAIL because `sqlite.Open` and `sqlite.Migrate` do not exist.

- [ ] **Step 3: Create the complete initial schema**

`00001_initial.sql` must create these tables with text UUID primary keys,
UTC timestamps stored as RFC 3339 text, foreign keys, and the listed
constraints:

```sql
-- +goose Up
CREATE TABLE admins (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    admin_id TEXT NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    token_hash BLOB NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    revoked_at TEXT
);
CREATE TABLE locations (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);
CREATE TABLE monitors (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind = 'http'),
    interval_ms INTEGER NOT NULL CHECK (interval_ms > 0),
    timeout_ms INTEGER NOT NULL CHECK (timeout_ms > 0 AND timeout_ms < interval_ms),
    failure_threshold INTEGER NOT NULL CHECK (failure_threshold > 0),
    recovery_threshold INTEGER NOT NULL CHECK (recovery_threshold > 0),
    http_json BLOB NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    next_run_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE monitor_locations (
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    location_id TEXT NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    required INTEGER NOT NULL CHECK (required IN (0, 1)),
    PRIMARY KEY (monitor_id, location_id)
);
CREATE TABLE agents (
    id TEXT PRIMARY KEY,
    location_id TEXT NOT NULL REFERENCES locations(id),
    name TEXT NOT NULL,
    credential_hash BLOB NOT NULL UNIQUE,
    credential_generation INTEGER NOT NULL,
    capabilities_json BLOB NOT NULL,
    version TEXT,
    last_seen_at TEXT,
    revoked_at TEXT,
    created_at TEXT NOT NULL
);
CREATE TABLE agent_enrollment_tokens (
    id TEXT PRIMARY KEY,
    location_id TEXT NOT NULL REFERENCES locations(id),
    token_hash BLOB NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    consumed_at TEXT,
    created_at TEXT NOT NULL
);
CREATE TABLE check_runs (
    id TEXT PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id),
    location_id TEXT NOT NULL REFERENCES locations(id),
    scheduled_for TEXT NOT NULL,
    probe_json BLOB NOT NULL,
    timeout_ms INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('available', 'leased', 'resolved')),
    lease_agent_id TEXT REFERENCES agents(id),
    lease_token_hash BLOB,
    lease_attempt INTEGER NOT NULL DEFAULT 0,
    lease_expires_at TEXT,
    resolved_at TEXT,
    UNIQUE (monitor_id, location_id, scheduled_for)
);
CREATE TABLE probe_results (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL UNIQUE REFERENCES check_runs(id),
    agent_id TEXT NOT NULL REFERENCES agents(id),
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    received_at TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('passed', 'failed')),
    latency_ms INTEGER NOT NULL,
    observed_status INTEGER,
    body_assertion_passed INTEGER,
    error_code TEXT,
    diagnostic_sample TEXT
);
CREATE TABLE location_health (
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    location_id TEXT NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    consecutive_failures INTEGER NOT NULL,
    consecutive_successes INTEGER NOT NULL,
    last_observed_at TEXT,
    last_transition_at TEXT,
    PRIMARY KEY (monitor_id, location_id)
);
CREATE TABLE monitor_health (
    monitor_id TEXT PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    last_transition_at TEXT
);
CREATE TABLE incidents (
    id TEXT PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id),
    state TEXT NOT NULL,
    severity TEXT NOT NULL,
    opened_at TEXT NOT NULL,
    last_transition_at TEXT NOT NULL,
    recovered_at TEXT
);
CREATE UNIQUE INDEX one_active_incident_per_monitor
    ON incidents(monitor_id) WHERE recovered_at IS NULL;
CREATE TABLE incident_events (
    id TEXT PRIMARY KEY,
    incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    previous_state TEXT,
    state TEXT NOT NULL,
    severity TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX available_runs ON check_runs(status, scheduled_for, lease_expires_at);

-- +goose Down
DROP TABLE incident_events;
DROP TABLE incidents;
DROP TABLE monitor_health;
DROP TABLE location_health;
DROP TABLE probe_results;
DROP TABLE check_runs;
DROP TABLE agent_enrollment_tokens;
DROP TABLE agents;
DROP TABLE monitor_locations;
DROP TABLE monitors;
DROP TABLE locations;
DROP TABLE sessions;
DROP TABLE admins;
```

- [ ] **Step 4: Add sqlc queries and generate**

Configure `sqlc.yaml`:

```yaml
version: "2"
sql:
  - engine: sqlite
    schema: db/migrations/sqlite
    queries: db/queries/sqlite
    gen:
      go:
        package: dbsqlite
        out: db/generated/sqlite
        emit_interface: true
        emit_json_tags: true
        emit_empty_slices: true
```

Every mutation and lookup used by Tasks 6-11 must have a named query. Required
atomic queries include:

```sql
-- name: DatabaseNow :one
SELECT strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: InsertScheduledRun :execrows
INSERT INTO check_runs (
  id, monitor_id, location_id, scheduled_for, probe_json, timeout_ms, status
) VALUES (?, ?, ?, ?, ?, ?, 'available')
ON CONFLICT (monitor_id, location_id, scheduled_for) DO NOTHING;

-- name: ClaimHTTPRun :one
UPDATE check_runs
SET status = 'leased',
    lease_agent_id = sqlc.arg(agent_id),
    lease_token_hash = sqlc.arg(lease_token_hash),
    lease_attempt = lease_attempt + 1,
    lease_expires_at = sqlc.arg(lease_expires_at)
WHERE id = (
  SELECT r.id
  FROM check_runs r
  JOIN agents a ON a.id = sqlc.arg(agent_id)
  WHERE r.location_id = a.location_id
    AND r.status IN ('available', 'leased')
    AND (r.status = 'available' OR r.lease_expires_at <= sqlc.arg(now))
    AND a.revoked_at IS NULL
  ORDER BY r.scheduled_for, r.id
  LIMIT 1
)
RETURNING *;
```

Add queries for admin/session lookup, Location and Monitor creation/get,
enrollment-token consumption, Agent heartbeat, result insert, run resolution,
health get/upsert/list, and Incident get/open/change/recover/event insertion.

Run: `go tool sqlc generate`

Expected: committed Go output under `db/generated/sqlite`.

- [ ] **Step 5: Implement connection, migrations, and mapped repositories**

`sqlite.Open`:

```go
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil { return nil, err }
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return db, nil
}
```

Embed migrations in `db/migrations/sqlite/migrations.go`, set goose's base FS,
dialect `sqlite3`, and table name `schema_migrations`. `Store.WithinTx` begins a
transaction, constructs repositories using `Queries.WithTx(tx)`, rolls back on
callback error, and commits otherwise. Mapping helpers must parse RFC 3339
timestamps and translate sqlc records into domain/application records.

- [ ] **Step 6: Run persistence verification**

Run:

```bash
go test ./internal/adapters/sqlite
go tool sqlc diff
go test ./...
```

Expected: migration and transaction tests PASS; `sqlc diff` reports no drift.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum sqlc.yaml db internal/application/store.go internal/adapters/sqlite
git commit -m "feat(sqlite): add first observation persistence"
```

---

### Task 6: Administrator bootstrap and session authentication

**Files:**
- Create: `internal/application/auth.go`
- Create: `internal/application/auth_test.go`
- Create: `internal/adapters/crypto/password.go`
- Create: `internal/adapters/crypto/password_test.go`
- Create: `internal/adapters/crypto/token.go`
- Create: `internal/adapters/crypto/token_test.go`
- Create: `internal/adapters/httpapi/auth.go`
- Create: `internal/adapters/httpapi/auth_test.go`
- Modify: `internal/application/store.go`
- Modify: `internal/adapters/sqlite/store.go`
- Modify: `db/queries/sqlite/auth.sql`
- Modify: `db/generated/sqlite/`

**Interfaces:**
- Produces: `application.AuthService.BootstrapAdmin(ctx, email, password) error`
- Produces: `application.AuthService.CreateSession(ctx, email, password) (application.SessionCredential, error)`
- Produces: `application.AuthService.AuthenticateSession(ctx, rawToken) (application.Principal, error)`
- Produces: `crypto.PasswordHasher` and `crypto.TokenIssuer`
- Consumes: `application.AdminRepository`, `SessionRepository`, `Store`

- [ ] **Step 1: Write failing service tests**

Use an in-memory fake Store and assert:

```go
func TestBootstrapAdminRefusesSecondAdministrator(t *testing.T) {
	service := newAuthServiceForTest(t)
	ctx := context.Background()
	if err := service.BootstrapAdmin(ctx, "admin@example.com", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	err := service.BootstrapAdmin(ctx, "other@example.com", "another correct horse battery staple")
	if !errors.Is(err, application.ErrAlreadyBootstrapped) {
		t.Fatalf("error = %v", err)
	}
}

func TestCreateAndAuthenticateSession(t *testing.T) {
	service := newAuthServiceForTest(t)
	ctx := context.Background()
	mustBootstrap(t, service)
	credential, err := service.CreateSession(ctx, "admin@example.com", testPassword)
	if err != nil { t.Fatal(err) }
	principal, err := service.AuthenticateSession(ctx, credential.Token)
	if err != nil { t.Fatal(err) }
	if principal.Kind != application.PrincipalAdmin {
		t.Fatalf("kind = %s", principal.Kind)
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/application ./internal/adapters/crypto`

Expected: FAIL because authentication services do not exist.

- [ ] **Step 3: Implement password and token primitives**

Pin `golang.org/x/crypto@v0.54.0`.

Run: `go get golang.org/x/crypto@v0.54.0`

Define application-side ports so the use case does not import the crypto
adapter:

```go
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(encodedHash, password string) bool
}
type IssuedToken struct {
	Raw string
	Hash []byte
}
type TokenIssuer interface {
	New() (IssuedToken, error)
	Hash(raw string) []byte
}
```

Use Argon2id parameters:

```go
type PasswordParams struct {
	Memory uint32
	Iterations uint32
	Parallelism uint8
	SaltLength uint32
	KeyLength uint32
}

var ProductionPasswordParams = PasswordParams{
	Memory: 64 * 1024, Iterations: 3, Parallelism: 2,
	SaltLength: 16, KeyLength: 32,
}
```

Encode hashes as
`$argon2id$v=19$m=65536,t=3,p=2$<base64-salt>$<base64-key>` and verify with
`subtle.ConstantTimeCompare`. Tests use smaller parameters but exercise the
same encoding/parser.

`TokenIssuer.New()` reads 32 bytes from `crypto/rand`, returns
`base64.RawURLEncoding` text to the caller, and returns `sha256.Sum256(raw)` for
storage. The raw token is never persisted.

- [ ] **Step 4: Implement AuthService and bearer middleware**

`BootstrapAdmin` validates a normalized email and a password of at least 16
Unicode code points, hashes it outside the transaction, then atomically refuses
creation if any admin exists.

`CreateSession` returns the same `ErrInvalidCredentials` for missing email and
bad password, creates a 12-hour session using the injected clock, and stores
only the token hash.

`AuthenticateSession` hashes the presented token, loads an unexpired,
unrevoked session, and returns:

```go
type PrincipalKind string
const (
	PrincipalAdmin PrincipalKind = "admin"
	PrincipalAgent PrincipalKind = "agent"
)
type Principal struct { Kind PrincipalKind; SubjectID string }
```

The HTTP middleware parses `Authorization: Bearer`, calls the appropriate
authenticator, and places the Principal in request context. It never logs the
header.

- [ ] **Step 5: Run and commit**

Run:

```bash
go tool sqlc generate
go test ./internal/application ./internal/adapters/crypto ./internal/adapters/httpapi ./internal/adapters/sqlite
```

Expected: PASS.

```bash
git add go.mod go.sum db internal
git commit -m "feat(auth): bootstrap admin sessions"
```

---

### Task 7: Location and Monitor application use cases

**Files:**
- Create: `internal/application/configuration.go`
- Test: `internal/application/configuration_test.go`
- Create: `internal/adapters/httpapi/configuration.go`
- Test: `internal/adapters/httpapi/configuration_test.go`
- Modify: `internal/application/store.go`
- Modify: `internal/adapters/sqlite/store.go`
- Modify: `db/queries/sqlite/configuration.sql`
- Modify: `db/generated/sqlite/`

**Interfaces:**
- Produces: `ConfigurationService.CreateLocation`
- Produces: `ConfigurationService.CreateHTTPMonitor`
- Produces: `ConfigurationService.GetMonitor`
- Produces strict handler methods `CreateLocation`, `CreateMonitor`, `GetMonitor`
- Consumes: Task 3 domain constructors, `Store`, `Clock`, and `IDGenerator`

- [ ] **Step 1: Write failing use-case tests**

```go
func TestCreateHTTPMonitorPersistsAssignmentAndInitialHealth(t *testing.T) {
	store := newFakeStore()
	clock := fixedClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	service := application.NewConfigurationService(store, clock, sequenceIDs("m1"))
	store.locations["l1"] = domain.Location{ID: "l1", Name: "public"}

	monitor, err := service.CreateHTTPMonitor(context.Background(), application.CreateHTTPMonitorCommand{
		Name: "website", LocationID: "l1", RequiredLocation: true,
		Interval: time.Minute, Timeout: 5 * time.Second,
		FailureThreshold: 3, RecoveryThreshold: 2,
		HTTP: domain.HTTPProbe{Method: "GET", URL: "https://example.com/health",
			ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}}},
	})
	if err != nil { t.Fatal(err) }
	if monitor.ID != "m1" { t.Fatalf("ID = %s", monitor.ID) }
	if got := store.monitorHealth["m1"].State; got != domain.HealthPending {
		t.Fatalf("initial health = %s", got)
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/application -run 'TestCreateHTTPMonitor|TestCreateLocation'`

Expected: FAIL because ConfigurationService is undefined.

- [ ] **Step 3: Implement application commands transactionally**

```go
type CreateHTTPMonitorCommand struct {
	Name string
	LocationID domain.LocationID
	RequiredLocation bool
	Interval time.Duration
	Timeout time.Duration
	FailureThreshold uint16
	RecoveryThreshold uint16
	HTTP domain.HTTPProbe
}
```

`CreateHTTPMonitor` loads the Location, builds the domain Monitor, and in one
transaction inserts the Monitor, assignment, pending LocationHealth, and
pending MonitorHealth. `next_run_at` equals the injected clock's current UTC
time so the first scheduler tick can enqueue it immediately.

Map duplicate names and missing locations to typed application errors:
`ErrConflict`, `ErrNotFound`, and `ValidationError{Fields map[string]string}`.

- [ ] **Step 4: Implement strict HTTP mappings**

Convert seconds/milliseconds at the adapter boundary. Parse generated UUID
strings into domain ID aliases without importing generated types into the
application package. Return generated `201`, `200`, and RFC 9457 default
response objects.

Direct handler test:

```go
response, err := server.CreateLocation(ctx, httpapi.CreateLocationRequestObject{
	Body: &httpapi.CreateLocationJSONRequestBody{Name: "public"},
})
if err != nil { t.Fatal(err) }
created, ok := response.(httpapi.CreateLocation201JSONResponse)
if !ok || created.Name != "public" {
	t.Fatalf("response = %#v", response)
}
```

- [ ] **Step 5: Generate, test, and commit**

Run:

```bash
go tool sqlc generate
go test ./internal/application ./internal/adapters/httpapi ./internal/adapters/sqlite
```

Expected: PASS.

```bash
git add db internal
git commit -m "feat(monitors): create locations and HTTP monitors"
```

---

### Task 8: One-time Agent enrollment and heartbeat

**Files:**
- Create: `internal/domain/agent.go`
- Test: `internal/domain/agent_test.go`
- Create: `internal/application/agents.go`
- Test: `internal/application/agents_test.go`
- Create: `internal/adapters/httpapi/agents.go`
- Test: `internal/adapters/httpapi/agents_test.go`
- Modify: `internal/application/store.go`
- Modify: `internal/adapters/sqlite/store.go`
- Modify: `db/queries/sqlite/agents.sql`
- Modify: `db/generated/sqlite/`

**Interfaces:**
- Produces: `AgentService.CreateEnrollmentToken`
- Produces: `AgentService.Enroll`
- Produces: `AgentService.Authenticate`
- Produces: `AgentService.Heartbeat`
- Produces strict handler methods for the four Agent operations
- Consumes: Task 6 TokenIssuer, Task 5 AgentRepository, Task 3 LocationID

- [ ] **Step 1: Write failing enrollment tests**

```go
func TestEnrollmentTokenIsOneTimeAndLocationScoped(t *testing.T) {
	service := newAgentServiceForTest(t)
	ctx := context.Background()
	enrollment, err := service.CreateEnrollmentToken(ctx, "l1", 15*time.Minute)
	if err != nil { t.Fatal(err) }

	first, err := service.Enroll(ctx, application.EnrollAgentCommand{
		Token: enrollment.Token, Name: "vps-1",
		Capabilities: []domain.AgentCapability{domain.CapabilityHTTP},
	})
	if err != nil { t.Fatal(err) }
	if first.LocationID != "l1" { t.Fatalf("location = %s", first.LocationID) }

	_, err = service.Enroll(ctx, application.EnrollAgentCommand{
		Token: enrollment.Token, Name: "vps-2",
		Capabilities: []domain.AgentCapability{domain.CapabilityHTTP},
	})
	if !errors.Is(err, application.ErrInvalidEnrollmentToken) {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/domain ./internal/application -run 'Agent|Enrollment|Heartbeat'`

Expected: FAIL because Agent types and services do not exist.

- [ ] **Step 3: Implement Agent domain and service**

```go
type AgentCapability string
const CapabilityHTTP AgentCapability = "http"

type Agent struct {
	ID AgentID
	LocationID LocationID
	Name string
	Capabilities []AgentCapability
	CredentialGeneration uint64
	Version string
	LastSeenAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}
```

Extend the application ports:

```go
type EnrollmentTokenRecord struct {
	ID string
	LocationID domain.LocationID
	TokenHash []byte
	ExpiresAt time.Time
	ConsumedAt *time.Time
	CreatedAt time.Time
}
type AgentRecord struct {
	Agent domain.Agent
	CredentialHash []byte
}
type AgentRepository interface {
	CreateEnrollmentToken(context.Context, EnrollmentTokenRecord) error
	ConsumeEnrollmentToken(context.Context, []byte, time.Time, time.Time) (EnrollmentTokenRecord, bool, error)
	Create(context.Context, AgentRecord) error
	Get(context.Context, domain.AgentID) (AgentRecord, error)
	FindActiveByCredentialHash(context.Context, []byte) (AgentRecord, error)
	UpdateHeartbeat(context.Context, domain.AgentID, uint64, string, []domain.AgentCapability, time.Time) (bool, error)
}
```

Add `Agents AgentRepository` to `application.Repositories` and implement it in
both the fake Store and SQLite Store in the same task.

Enrollment-token creation clamps TTL to 1-60 minutes and defaults to 15
minutes. `Enroll` hashes the submitted raw token, then inside one transaction:
loads an unconsumed, unexpired token; marks it consumed with compare-and-set;
creates the Agent; and stores only the new credential hash. The response
contains the raw credential once.

`Heartbeat` requires a valid Agent principal, confirms the presented credential
generation, and updates version, capabilities, and `last_seen_at`.

- [ ] **Step 4: Implement HTTP handlers and Agent auth**

`CreateAgentEnrollmentToken` requires an admin Principal.
`EnrollAgent` has no bearer auth because the one-time token is the credential.
Heartbeat and later work/result operations require an Agent bearer Principal.
Return the same unauthorized problem for missing, invalid, expired, or revoked
credentials.

- [ ] **Step 5: Generate, test, and commit**

Run:

```bash
go tool sqlc generate
go test ./internal/domain ./internal/application ./internal/adapters/httpapi ./internal/adapters/sqlite
```

Expected: PASS.

```bash
git add db internal
git commit -m "feat(agents): enroll and authenticate agents"
```

---

### Task 9: Scheduler and crash-safe HTTP work leasing

**Files:**
- Create: `internal/application/scheduler.go`
- Test: `internal/application/scheduler_test.go`
- Create: `internal/application/leasing.go`
- Test: `internal/application/leasing_test.go`
- Create: `internal/adapters/httpapi/work.go`
- Test: `internal/adapters/httpapi/work_test.go`
- Modify: `internal/application/store.go`
- Modify: `internal/adapters/sqlite/store.go`
- Modify: `db/queries/sqlite/runs.sql`
- Modify: `db/generated/sqlite/`

**Interfaces:**
- Produces: `Scheduler.EnqueueDue(ctx, limit int) (int, error)`
- Produces: `LeaseService.LeaseHTTP(ctx, agentID, wait time.Duration) (*application.HTTPWork, error)`
- Produces: `application.ErrNoWork`
- Produces strict handler method `LeaseAgentWork`
- Consumes: Monitor schedule and Agent capability records

- [ ] **Step 1: Write failing scheduler and lease tests**

```go
func TestEnqueueDueIsIdempotent(t *testing.T) {
	service, store := newSchedulerForTest(t)
	ctx := context.Background()
	first, err := service.EnqueueDue(ctx, 100)
	if err != nil { t.Fatal(err) }
	second, err := service.EnqueueDue(ctx, 100)
	if err != nil { t.Fatal(err) }
	if first != 1 || second != 0 || len(store.runs) != 1 {
		t.Fatalf("first=%d second=%d runs=%d", first, second, len(store.runs))
	}
}

func TestExpiredLeaseCanBeReclaimedByCompatibleAgent(t *testing.T) {
	service, clock := newLeaseServiceForTest(t)
	first, err := service.LeaseHTTP(context.Background(), "a1", 0)
	if err != nil { t.Fatal(err) }
	clock.Advance(31 * time.Second)
	second, err := service.LeaseHTTP(context.Background(), "a2", 0)
	if err != nil { t.Fatal(err) }
	if first.RunID != second.RunID || first.LeaseToken == second.LeaseToken {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/application -run 'EnqueueDue|LeaseHTTP'`

Expected: FAIL because Scheduler and LeaseService do not exist.

- [ ] **Step 3: Implement scheduling**

```go
type HTTPWork struct {
	RunID domain.CheckRunID
	MonitorID domain.MonitorID
	ScheduledFor time.Time
	LeaseToken string
	Timeout time.Duration
	Probe domain.HTTPProbe
}
```

Extend the application ports:

```go
type DueMonitor struct {
	Monitor domain.Monitor
	LocationID domain.LocationID
	Required bool
	NextRunAt time.Time
}
type NewRunRecord struct {
	ID domain.CheckRunID
	MonitorID domain.MonitorID
	LocationID domain.LocationID
	ScheduledFor time.Time
	Probe domain.HTTPProbe
	Timeout time.Duration
}
type ClaimRunParams struct {
	AgentID domain.AgentID
	LeaseTokenHash []byte
	LeaseExpiresAt time.Time
	Now time.Time
}
type RunRecord struct {
	ID domain.CheckRunID
	MonitorID domain.MonitorID
	LocationID domain.LocationID
	ScheduledFor time.Time
	Probe domain.HTTPProbe
	Timeout time.Duration
	Status string
	LeaseAgentID domain.AgentID
	LeaseTokenHash []byte
	LeaseAttempt uint32
	LeaseExpiresAt *time.Time
	ResolvedAt *time.Time
}
type RunRepository interface {
	DatabaseNow(context.Context) (time.Time, error)
	Insert(context.Context, NewRunRecord) (bool, error)
	ClaimHTTP(context.Context, ClaimRunParams) (RunRecord, error)
	Get(context.Context, domain.CheckRunID) (RunRecord, error)
	Resolve(context.Context, domain.CheckRunID, domain.AgentID, []byte, time.Time) (bool, error)
}
```

Extend `MonitorRepository` with
`ListDue(context.Context, time.Time, int) ([]DueMonitor, error)` and
`AdvanceNextRun(context.Context, domain.MonitorID, time.Time, time.Time) (bool, error)`.
Add `Runs RunRepository` to `Repositories`.

`EnqueueDue` loads at most `limit` due Monitor assignments, serializes an
immutable HTTP probe snapshot, inserts the unique CheckRun, and advances
`next_run_at` by exact interval multiples until it is after `now`. It advances
the schedule even when the unique run already exists, preventing a stuck due
row. Limit defaults to 100 and cannot exceed 1000.

- [ ] **Step 4: Implement atomic leasing and bounded long-poll**

`LeaseHTTP` verifies the Agent is active and has `http` capability, asks
`RunRepository.DatabaseNow` for authoritative database time, generates a new
opaque lease token, and invokes the atomic `ClaimHTTPRun` query. It stores only
the lease-token hash and sets expiry to `databaseNow + LeaseDuration`. Fake
repositories return the injected fake time from `DatabaseNow`, while SQLite
uses `strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`.

For HTTP requests, repeat the claim every 250ms until work is found, the request
context is cancelled, or `waitSeconds` elapses. Clamp `waitSeconds` to 0-30.
Return HTTP 204 for `ErrNoWork`; do not hold a database transaction while
waiting.

- [ ] **Step 5: Add SQLite competition tests**

Start two goroutines calling `LeaseHTTP` for two Agents in the same Location.
Assert only one receives the available run and the other receives
`ErrNoWork`. Advance a fake clock past expiry and assert the second Agent can
reclaim the same run with attempt 2 and a different token.

- [ ] **Step 6: Generate, test, and commit**

Run:

```bash
go tool sqlc generate
go test -race ./internal/application ./internal/adapters/httpapi ./internal/adapters/sqlite
```

Expected: PASS with no race reports.

```bash
git add db internal
git commit -m "feat(scheduler): lease crash-safe HTTP work"
```

---

### Task 10: Independent Agent HTTP executor and worker loop

**Files:**
- Create: `agent/oapi-codegen.yaml`
- Create: `agent/internal/controlplane/generate.go`
- Create: `agent/internal/controlplane/generated.gen.go`
- Create: `agent/probe/policy.go`
- Test: `agent/probe/policy_test.go`
- Create: `agent/probe/http.go`
- Test: `agent/probe/http_test.go`
- Create: `agent/worker/worker.go`
- Test: `agent/worker/worker_test.go`
- Create: `agent/cmd/xisnove-agent/main.go`
- Modify: `agent/go.mod`

**Interfaces:**
- Produces: `probe.HTTPExecutor.Execute(context.Context, controlplane.HTTPWork) controlplane.ProbeResultInput`
- Produces: `worker.Worker.RunOnce(context.Context) error`
- Consumes: only the Agent's generated control-plane client and standard library

- [ ] **Step 1: Generate an Agent-internal API client**

Configuration:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/oapi-codegen/oapi-codegen/v2.8.0/configuration-schema.json
package: controlplane
generate:
  models: true
  client: true
output: generated.gen.go
```

Directive:

```go
package controlplane

//go:generate go tool oapi-codegen -config ../../oapi-codegen.yaml ../../../api/openapi.yaml
```

Pin `oapi-codegen` v2.8.0 as an Agent module tool and run:

```bash
cd agent
go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0
go generate ./internal/controlplane
GOWORK=off go test ./...
```

Expected: generated client compiles without a dependency on the root module.

- [ ] **Step 2: Write failing HTTP execution policy tests**

```go
func TestHTTPExecutorEvaluatesStatusAndBody(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, "not ready")
	}))
	defer target.Close()

	executor := probe.NewHTTPExecutor(probe.Policy{
		AllowedPrivate: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		MaxResponseBytes: 64 << 10,
		MaxRedirects: 3,
	})
	result := executor.Execute(context.Background(), testWork(target.URL, 200, 299, "ready"))
	if result.Outcome != controlplane.Failed {
		t.Fatalf("outcome = %s", result.Outcome)
	}
	if result.ObservedStatus == nil || *result.ObservedStatus != 503 {
		t.Fatalf("status = %#v", result.ObservedStatus)
	}
	if result.BodyAssertionPassed == nil || *result.BodyAssertionPassed {
		t.Fatalf("body assertion = %#v", result.BodyAssertionPassed)
	}
}

func TestHTTPExecutorDeniesMetadataAddress(t *testing.T) {
	executor := probe.NewHTTPExecutor(probe.DefaultPolicy())
	result := executor.Execute(context.Background(), testWork("http://169.254.169.254/latest", 200, 299, ""))
	if result.ErrorCode == nil || *result.ErrorCode != "target_denied" {
		t.Fatalf("result = %#v", result)
	}
}
```

- [ ] **Step 3: Implement SSRF-safe HTTP execution**

`Policy.ValidateTarget`:

- permits only `http` and `https`;
- resolves every hostname before dialing;
- denies unspecified, loopback, multicast, link-local, and private addresses
  unless covered by `AllowedPrivate`;
- always denies `169.254.169.254/32` and IPv6 link-local metadata targets;
- revalidates every redirect;
- clamps body reads to `MaxResponseBytes`;
- never returns headers or full response bodies.

Build a dedicated `http.Transport` whose `DialContext` connects to the
validated IP while preserving the original hostname for the HTTP Host header
and TLS ServerName. Do not validate with one DNS lookup and then let the
default transport perform a second lookup.

The executor derives a context timeout from `HTTPWork.timeoutMillis`, records
start/finish/latency, evaluates all status ranges and `bodyContains` values,
and returns a diagnostic sample capped at 512 UTF-8 bytes after replacing
control characters. Network errors use stable codes such as `dns_error`,
`connect_error`, `timeout`, `target_denied`, and `response_too_large`.

- [ ] **Step 4: Write failing worker-loop tests**

Use an `httptest.Server` implementing lease and batch endpoints. Assert
`RunOnce`:

1. sends Agent bearer auth;
2. leases one HTTPWork;
3. calls the executor;
4. uploads one result with a UUID result ID and returned lease token;
5. treats `accepted` and `duplicate` acknowledgements as success;
6. returns nil on HTTP 204 no-work.

- [ ] **Step 5: Implement the worker and command**

`Worker` owns:

```go
type Worker struct {
	Client *controlplane.ClientWithResponses
	Credential func() (string, error)
	Executor interface {
		Execute(context.Context, controlplane.HTTPWork) controlplane.ProbeResultInput
	}
	Version string
}
```

`RunOnce` sends a heartbeat, performs a 30-second lease call, executes work,
and retries result upload with capped exponential backoff while the process is
alive. It uses a bounded channel of 100 results and never writes credentials or
results to disk.

The command reads `XISNOVE_URL`, `XISNOVE_AGENT_CREDENTIAL_FILE`, and
`XISNOVE_AGENT_ALLOWED_PRIVATE_CIDRS`, constructs the client, and loops until
SIGINT/SIGTERM cancellation.

- [ ] **Step 6: Verify module independence and commit**

Run:

```bash
cd agent
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
cd ..
```

Expected: PASS without importing `github.com/araihu/xisnove/internal/...`.

```bash
git add agent
git commit -m "feat(agent): execute leased HTTP probes"
```

---

### Task 11: Idempotent result ingestion and atomic Incident transition

**Files:**
- Create: `internal/application/results.go`
- Test: `internal/application/results_test.go`
- Create: `internal/adapters/httpapi/results.go`
- Test: `internal/adapters/httpapi/results_test.go`
- Create: `internal/adapters/httpapi/health.go`
- Test: `internal/adapters/httpapi/health_test.go`
- Modify: `internal/application/store.go`
- Modify: `internal/adapters/sqlite/store.go`
- Modify: `db/queries/sqlite/results.sql`
- Modify: `db/queries/sqlite/health.sql`
- Modify: `db/queries/sqlite/incidents.sql`
- Modify: `db/generated/sqlite/`

**Interfaces:**
- Produces: `ResultService.UploadBatch(ctx, agentID, []application.ProbeResultCommand) ([]application.ResultAcknowledgement, error)`
- Produces: `HealthService.GetMonitorHealth`
- Produces: `HealthService.GetActiveIncident`
- Produces strict handler methods `UploadProbeResults`, `GetMonitorHealth`, `GetActiveMonitorIncident`
- Consumes: Task 4 health/Incident decisions and Task 5 transactional Store

- [ ] **Step 1: Write failing ingestion tests**

```go
func TestThirdFailureOpensOneIncidentAndDuplicateIsHarmless(t *testing.T) {
	service, store := newResultServiceForTest(t, thresholds(3, 2))
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		run, lease := store.addLeasedRun("m1", "l1", "a1")
		acks, err := service.UploadBatch(ctx, "a1", []application.ProbeResultCommand{
			failedResult(fmt.Sprintf("r%d", i), run, lease),
		})
		if err != nil { t.Fatal(err) }
		if acks[0].Status != application.ResultAccepted { t.Fatalf("ack = %#v", acks[0]) }
	}
	if got := store.monitorHealth["m1"].State; got != domain.HealthDown {
		t.Fatalf("health = %s", got)
	}
	if len(store.activeIncidents) != 1 || len(store.incidentEvents) != 1 {
		t.Fatalf("incidents=%d events=%d", len(store.activeIncidents), len(store.incidentEvents))
	}

	acks, err := service.UploadBatch(ctx, "a1", []application.ProbeResultCommand{
		failedResult("r3", store.lastRunID, store.lastLeaseToken),
	})
	if err != nil { t.Fatal(err) }
	if acks[0].Status != application.ResultDuplicate {
		t.Fatalf("ack = %#v", acks[0])
	}
	if len(store.incidentEvents) != 1 {
		t.Fatalf("duplicate created event: %d", len(store.incidentEvents))
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/application -run 'ThirdFailure|UploadBatch'`

Expected: FAIL because ResultService does not exist.

- [ ] **Step 3: Implement one transaction per result**

Extend the application ports:

```go
type ProbeResultRecord struct {
	ID string
	RunID domain.CheckRunID
	AgentID domain.AgentID
	StartedAt, FinishedAt, ReceivedAt time.Time
	Passed bool
	Latency time.Duration
	ObservedStatus *int
	BodyAssertionPassed *bool
	ErrorCode, DiagnosticSample string
}
type ResultRepository interface {
	GetByID(context.Context, string) (ProbeResultRecord, error)
	GetByRun(context.Context, domain.CheckRunID) (ProbeResultRecord, error)
	Insert(context.Context, ProbeResultRecord) (bool, error)
}
type IncidentRepository interface {
	GetActive(context.Context, domain.MonitorID) (*domain.Incident, error)
	Open(context.Context, domain.Incident) error
	Update(context.Context, domain.Incident) error
	AppendEvent(context.Context, domain.IncidentEvent) error
}
```

Add `Results ResultRepository` and `Incidents IncidentRepository` to
`Repositories`.

For each result, `UploadBatch` starts a transaction and:

1. looks up the result ID and immediately acknowledges `duplicate` when it
   already exists;
2. loads the run and acknowledges `duplicate` when another valid result has
   already resolved that run, even if the retry uses a different result ID;
3. verifies Agent ID, active lease, constant-time lease-token hash, and lease
   expiry;
4. inserts ProbeResult with unique result and run IDs;
5. marks the run resolved with compare-and-set;
6. loads Monitor thresholds and current LocationHealth;
7. applies `domain.ApplyProbe` and upserts LocationHealth;
8. loads all required LocationHealth rows and calls `RollupRequired`;
9. updates MonitorHealth only if state or observation time changed;
10. calls `DecideIncident`;
11. opens/changes/recovers the Incident and appends one IncidentEvent when the
    decision action is not `none`;
12. commits, then returns `accepted`.

If any step fails, rollback leaves the run claimable until lease expiry and no
partial health or Incident state is visible.

Result validation rejects timestamps outside the lease window plus a 30-second
clock-skew allowance, negative latency, samples over 512 bytes, and unsupported
outcomes.

- [ ] **Step 4: Implement health and Incident queries**

`GetMonitorHealth` returns aggregate state plus every assigned Location state.
`GetActiveIncident` returns nil for no active Incident. API adapters map nil to
HTTP 204 and avoid manufacturing an empty Incident.

- [ ] **Step 5: Add real SQLite atomicity tests**

Against a temporary migrated database:

- upload the same result concurrently twice and assert one `accepted`, one
  `duplicate`, one ProbeResult, and one projection update;
- force an Incident insert error inside the transaction and assert result,
  resolved run, and health writes roll back;
- upload three distinct failures and assert the partial unique index permits
  exactly one active Incident.

- [ ] **Step 6: Generate, test, and commit**

Run:

```bash
go tool sqlc generate
go test -race ./internal/application ./internal/adapters/httpapi ./internal/adapters/sqlite
```

Expected: PASS.

```bash
git add db internal
git commit -m "feat(results): derive incidents from probe results"
```

---

### Task 12: Server composition and first-observation integration test

**Files:**
- Create: `internal/adapters/httpapi/server.go`
- Create: `internal/adapters/httpapi/problems.go`
- Test: `internal/adapters/httpapi/problems_test.go`
- Create: `internal/adapters/clock/system.go`
- Create: `internal/adapters/ids/uuid.go`
- Create: `cmd/xisnove-server/main.go`
- Create: `cmd/xisnove-server/serve.go`
- Create: `cmd/xisnove-server/database.go`
- Create: `cmd/xisnove-server/admin.go`
- Test: `cmd/xisnove-server/main_test.go`
- Create: `integration/first_observation_test.go`

**Interfaces:**
- Produces: a complete `httpapi.StrictServerInterface`
- Produces commands: `xisnove-server db migrate`, `xisnove-server admin bootstrap`, `xisnove-server serve`
- Consumes: all previous root-module services and adapters

- [ ] **Step 1: Write failing RFC 9457 mapping tests**

```go
func TestProblemMappingDoesNotLeakInternalError(t *testing.T) {
	problem := httpapi.ToProblem(
		errors.New("sqlite: secret path /private/db failed"),
		"correlation-1",
	)
	if problem.Status != 500 || problem.Code != "internal_error" {
		t.Fatalf("problem = %#v", problem)
	}
	if strings.Contains(problem.Detail, "sqlite") || strings.Contains(problem.Detail, "/private") {
		t.Fatalf("leaked detail: %q", problem.Detail)
	}
}
```

- [ ] **Step 2: Compose the strict server**

`Server` owns service pointers and satisfies the generated interface:

```go
var _ StrictServerInterface = (*Server)(nil)

type Server struct {
	Auth *application.AuthService
	Configuration *application.ConfigurationService
	Agents *application.AgentService
	Leases *application.LeaseService
	Results *application.ResultService
	Health *application.HealthService
}
```

Load the embedded OpenAPI spec and wrap the generated strict handler with:

1. correlation-ID middleware;
2. structured `slog` request logging that excludes authorization and bodies;
3. `nethttp-middleware` request validation;
4. operation-aware admin/Agent bearer authentication;
5. panic recovery returning RFC 9457.

Expose `/livez` unconditionally and `/readyz` only when the database pings and
the applied goose version equals the embedded latest version.

- [ ] **Step 3: Implement explicit commands**

Use the standard `flag` package:

```text
xisnove-server db migrate --database /path/xisnove.db
xisnove-server admin bootstrap --database /path/xisnove.db --email admin@example.com --password-file /run/secrets/admin_password
xisnove-server serve --database /path/xisnove.db --listen 127.0.0.1:8080
```

`db migrate` is the only command that applies schema changes. `serve` checks
schema compatibility and refuses to start if pending migrations exist.
`admin bootstrap` reads one trimmed password from the supplied file, never from
a command-line flag, and prints no credential.

`serve` runs:

- one scheduler tick immediately and every second;
- HTTP with read-header, request, idle, and shutdown timeouts;
- graceful SIGINT/SIGTERM shutdown that stops new long-polls before closing the
  database.

- [ ] **Step 4: Write the failing integration test**

The test uses the public SDK and the generated Agent endpoints over
`httptest.Server`. Use a fake clock and an HTTP target returning 503 with body
`not ready`.

```go
func TestFirstObservationOpensIncidentAfterThirdFailure(t *testing.T) {
	fixture := newSystemFixture(t)
	adminToken := fixture.bootstrapAndLogin(t)
	location := fixture.createLocation(t, adminToken, "public")
	monitor := fixture.createHTTPMonitor(t, adminToken, location.Id, fixture.target.URL)
	agentToken := fixture.createEnrollmentToken(t, adminToken, location.Id)
	agent := fixture.enrollAgent(t, agentToken.Token)

	for i := 0; i < 3; i++ {
		fixture.scheduler.EnqueueDue(fixture.ctx, 100)
		work := fixture.leaseWork(t, agent.Credential)
		result := executeHTTPWorkForIntegration(work)
		fixture.uploadResult(t, agent.Credential, result)
		fixture.clock.Advance(time.Minute)
	}

	health := fixture.getHealth(t, adminToken, monitor.Id)
	if health.State != sdk.Down {
		t.Fatalf("state = %s", health.State)
	}
	incident := fixture.getActiveIncident(t, adminToken, monitor.Id)
	if incident.Severity != sdk.Critical {
		t.Fatalf("severity = %s", incident.Severity)
	}
	fixture.uploadResult(t, agent.Credential, fixture.lastResult)
	fixture.assertOneIncidentEvent(t)
}
```

The integration helper performs the HTTP request locally without importing the
Agent module; Task 10 already verifies the independent Agent executor against
the same work/result schemas.

- [ ] **Step 5: Run the complete milestone test**

Run:

```bash
go test -race ./integration -run TestFirstObservationOpensIncidentAfterThirdFailure -v
go test -race ./...
cd agent && GOWORK=off go test -race ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd internal integration
git commit -m "feat(server): complete first observation path"
```

---

### Task 13: CI, generation gates, and operator-facing documentation

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `README.md`
- Create: `docs/development.md`
- Create: `docs/operations/first-observation.md`
- Modify: `Makefile`

**Interfaces:**
- Produces: reproducible contributor and operator commands
- Consumes: every verification command from Tasks 1-12

- [ ] **Step 1: Write the CI workflow**

The workflow has separate jobs:

```yaml
name: ci
on:
  pull_request:
  push:
    branches: [master]

jobs:
  root:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4
        with: {fetch-depth: 0}
      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6
        with:
          go-version: "1.26.1"
          cache-dependency-path: |
            go.sum
            agent/go.sum
      - run: go tool vacuum lint -d api/openapi.yaml
      - run: go generate ./...
      - run: go tool sqlc diff
      - run: git diff --exit-code
      - run: go vet ./...
      - run: go test -race ./...
  agent:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: agent
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4
      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6
        with:
          go-version: "1.26.1"
          cache-dependency-path: agent/go.sum
      - run: GOWORK=off go generate ./...
      - run: git diff --exit-code
      - run: GOWORK=off go vet ./...
      - run: GOWORK=off go test -race ./...
```

Add a pull-request-only API compatibility step that writes the base branch's
`api/openapi.yaml` to a temporary file and runs:

```bash
go tool oasdiff breaking /tmp/base-openapi.yaml api/openapi.yaml
```

If the base branch predates the file, the step reports that no compatibility
baseline exists and succeeds.

- [ ] **Step 2: Document exact local and first-run flows**

`README.md` describes the milestone honestly: HTTP/SQLite first observation,
not the entire v1 roadmap. Link to the approved design and this plan.

`docs/development.md` includes:

```bash
make generate
make test
make check
go run ./cmd/xisnove-server db migrate --database ./dev.db
go run ./cmd/xisnove-server admin bootstrap \
  --database ./dev.db \
  --email admin@example.com \
  --password-file ./dev-admin-password
go run ./cmd/xisnove-server serve --database ./dev.db
```

`docs/operations/first-observation.md` gives curl and SDK examples for login,
Location creation, Monitor creation, enrollment, Agent startup, health query,
and Incident query. It states that the Agent credential is shown once and
should be written to a mode-0600 file.

- [ ] **Step 3: Run the full verification suite**

Run:

```bash
make check
git status --short
```

Expected: all generation, lint, vet, race, root, Agent, and integration checks
pass; only the intended documentation and workflow files are uncommitted.

- [ ] **Step 4: Commit milestone documentation and CI**

```bash
git add .github Makefile README.md docs/development.md docs/operations/first-observation.md
git commit -m "ci: verify first observation milestone"
```

---

## Final milestone verification

Run from the repository root:

```bash
make check
go test -race ./integration -run TestFirstObservationOpensIncidentAfterThirdFailure -count=10
git status --short --branch
```

Expected:

- OpenAPI and sqlc generation are clean.
- Vacuum reports no OpenAPI errors.
- Root and Agent vet/race tests pass.
- The first-observation integration test passes ten consecutive runs.
- The worktree is clean.
- Commit history contains one focused commit per task.

Review the implementation against
`docs/superpowers/specs/2026-07-24-xisnove-v1-design.md`, but reject scope
expansion into later milestones even when the later feature appears easy.
