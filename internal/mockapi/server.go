package mockapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const (
	// Fixture credentials are intentionally public, deterministic mock values.
	FixtureAdminEmail            = "admin@xisnove.test"
	FixtureAdminPassword         = "mock-password"
	FixtureSessionToken          = "xisnove_mock_session_admin_0000000000000001"
	FixtureAgentToken            = "xisnove_mock_agent_000000000000000000000001"
	FixtureAgentTokenGeneration2 = "xisnove_mock_agent_rotated_000000000000000002"
	FixtureFullAPIToken          = "xisnove_mock_api_full_0000000000000000000001"
	FixtureReadOnlyAPIToken      = "xisnove_mock_api_read_0000000000000000000001"

	fixtureTime       = "2026-07-25T12:00:00Z"
	fixtureLocationID = "00000000-0000-4000-8000-000000000001"
)

type Server struct {
	mu sync.Mutex

	sessionActive                bool
	tokensByID                   map[string]*apiTokenRecord
	tokensByValue                map[string]*apiTokenRecord
	monitors                     []map[string]any
	incidents                    []map[string]any
	events                       map[string][]map[string]any
	candidates                   []map[string]any
	channels                     []map[string]any
	routes                       []map[string]any
	deliveries                   []map[string]any
	idempotency                  map[string]storedResponse
	idempotencyLocks             map[string]*sync.Mutex
	counters                     map[string]int
	agentCredentialsByToken      map[string]*mockAgentCredential
	agentCredentialsByGeneration map[int64]*mockAgentCredential
	currentAgentGeneration       int64
	operatorMonitors             map[string]mockOperatorBinding
	operatorAgents               map[string]*mockOperatorAgent
}

type mockAgentCredential struct {
	token      string
	generation int64
	observed   bool
	revoked    bool
}

type apiTokenRecord struct {
	ID        string
	Token     string
	Name      string
	Scopes    []string
	RevokedAt string
}

type mockOperatorBinding struct {
	ownerKey   string
	ownerUID   string
	externalID string
}

type mockOperatorAgent struct {
	mockOperatorBinding
	credentialGeneration int64
	credentialHashes     map[int64]string
}

type storedResponse struct {
	status      int
	contentType string
	body        []byte
	requestHash string
	credential  bool
}

type requestHashContextKey struct{}

type capturedRequest struct {
	body []byte
	hash string
}

type strictMockDispatcher struct {
	StrictServerInterface
}

func NewServer() *Server {
	server := &Server{
		tokensByID:                   map[string]*apiTokenRecord{},
		tokensByValue:                map[string]*apiTokenRecord{},
		events:                       map[string][]map[string]any{},
		idempotency:                  map[string]storedResponse{},
		idempotencyLocks:             map[string]*sync.Mutex{},
		agentCredentialsByToken:      map[string]*mockAgentCredential{},
		agentCredentialsByGeneration: map[int64]*mockAgentCredential{},
		operatorMonitors:             map[string]mockOperatorBinding{},
		operatorAgents:               map[string]*mockOperatorAgent{},
		currentAgentGeneration:       1,
		counters: map[string]int{
			"token": 1000, "monitor": 2000, "candidate": 3000, "channel": 4000,
		},
	}
	initialAgentCredential := &mockAgentCredential{token: FixtureAgentToken, generation: 1, observed: true}
	server.agentCredentialsByToken[initialAgentCredential.token] = initialAgentCredential
	server.agentCredentialsByGeneration[initialAgentCredential.generation] = initialAgentCredential
	server.seedFixtures()
	server.tokensByValue[FixtureFullAPIToken].Scopes = append(server.tokensByValue[FixtureFullAPIToken].Scopes, "operator:provision")
	server.operatorAgents[operatorOwnerIdentity("fixture/operator-agent", "fixture-agent-uid")] = &mockOperatorAgent{
		mockOperatorBinding:  mockOperatorBinding{ownerKey: "fixture/operator-agent", ownerUID: "fixture-agent-uid", externalID: fixtureAgentID},
		credentialGeneration: 2,
		credentialHashes: map[int64]string{
			1: mockCredentialHash("xisnove_mock_operator_fixture_credential_0001"),
			2: mockCredentialHash("xisnove_mock_operator_fixture_credential_0002"),
		},
	}
	return server
}

func (s *Server) Handler() http.Handler {
	spec, err := GetSwagger()
	if err != nil {
		panic(fmt.Sprintf("load embedded OpenAPI contract: %v", err))
	}
	strict := NewStrictHandlerWithOptions(
		&strictMockDispatcher{},
		[]StrictMiddlewareFunc{s.dispatchStrict},
		StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
				writeProblem(w, r, http.StatusBadRequest, "validation_failed", "Request validation failed", nil)
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
				writeProblem(w, r, http.StatusInternalServerError, "mock_response_failed", "Mock response failed", nil)
			},
		},
	)
	api := HandlerWithOptions(strict, StdHTTPServerOptions{BaseRouter: newOperatorActionServeMux()})
	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.serveScenario(w, r) {
			return
		}
		api.ServeHTTP(w, r)
	})
	conforming, err := newOpenAPIConformanceHandler(spec, dispatch)
	if err != nil {
		panic(fmt.Sprintf("create OpenAPI-conforming mock handler: %v", err))
	}
	return captureRequestHash(conforming)
}

// operatorActionServeMux preserves the OpenAPI action suffix while adapting it
// to net/http's wildcard grammar. oapi-codegen emits `{generation}:revoke`,
// which Go's ServeMux rejects; the wrapper removes the action suffix only from
// the captured path value before the generated handler parses it.
type operatorActionServeMux struct{ *http.ServeMux }

func newOperatorActionServeMux() *operatorActionServeMux {
	return &operatorActionServeMux{ServeMux: http.NewServeMux()}
}

func (m *operatorActionServeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	const action = "{generation}:revoke"
	if !strings.Contains(pattern, action) {
		m.ServeMux.HandleFunc(pattern, handler)
		return
	}
	pattern = strings.Replace(pattern, action, "{generation}", 1)
	m.ServeMux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		generation := r.PathValue("generation")
		if !strings.HasSuffix(generation, ":revoke") {
			http.NotFound(w, r)
			return
		}
		r.SetPathValue("generation", strings.TrimSuffix(generation, ":revoke"))
		handler(w, r)
	})
}

func (s *Server) dispatchStrict(_ StrictHandlerFunc, operationID string) StrictHandlerFunc {
	return func(_ context.Context, w http.ResponseWriter, r *http.Request, _ any) (any, error) {
		captured, _ := r.Context().Value(requestHashContextKey{}).(capturedRequest)
		r.Body = io.NopCloser(bytes.NewReader(captured.body))
		unlock := s.lockIdempotency(r)
		defer unlock()
		s.serveHTTP(w, r, operationID)
		return nil, nil
	}
}

func captureRequestHash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			captured := capturedRequest{hash: hashRequestBody(nil)}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestHashContextKey{}, captured)))
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeProblem(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "Request too large", nil)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		captured := capturedRequest{body: body, hash: hashRequestBody(body)}
		ctx := context.WithValue(r.Context(), requestHashContextKey{}, captured)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func hashRequestBody(body []byte) string {
	canonical := body
	if len(body) != 0 {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil {
			if encoded, err := json.Marshal(value); err == nil {
				canonical = encoded
			}
		}
	}
	sum := sha256.Sum256(canonical)
	return fmt.Sprintf("%x", sum[:])
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request, operationID string) {
	switch operationID {
	case "CreateSession":
		s.createSession(w, r)
	case "RevokeCurrentSession":
		s.revokeSession(w, r)
	case "CreateAPIToken", "ListAPITokens":
		s.apiTokens(w, r)
	case "GetAPIToken", "UpdateAPIToken", "RevokeAPIToken":
		s.apiToken(w, r, strings.TrimPrefix(r.URL.Path, "/v1/api-tokens/"))
	case "CreateMonitor", "ListMonitors":
		s.monitorCollection(w, r)
	case "GetMonitor", "UpdateMonitor", "DisableMonitor":
		s.monitor(w, r, strings.TrimPrefix(r.URL.Path, "/v1/monitors/"))
	case "ListIncidents":
		s.listIncidents(w, r)
	case "GetIncident", "ListIncidentEvents":
		s.incident(w, r, strings.TrimPrefix(r.URL.Path, "/v1/incidents/"))
	case "UpsertDiscoveryCandidates":
		s.upsertCompleteDiscoveryCandidates(w, r)
	case "ListDiscoveryCandidates":
		s.listDiscoveryCandidates(w, r)
	case "GetDiscoveryCandidate", "PromoteDiscoveryCandidate":
		s.discoveryCandidate(w, r, strings.TrimPrefix(r.URL.Path, "/v1/discovery-candidates/"))
	case "CreateNotificationChannel", "ListNotificationChannels":
		s.notificationChannels(w, r)
	case "ListNotificationRoutes":
		s.listNotificationRoutes(w, r)
	case "ListNotificationDeliveries":
		s.listNotificationDeliveries(w, r)
	case "GetPublicStatusPage":
		s.publicStatus(w, r)
	case "ObserveOperatorAgent":
		s.observeOperatorAgent(w, r)
	case "ApplyOperatorMonitor", "DeleteOperatorMonitor", "ApplyOperatorAgent",
		"PutOperatorAgentCredential", "RevokeOperatorAgentCredential", "DeleteOperatorAgent":
		s.operatorMutation(w, r, operationID)
	default:
		s.serveAdvertisedOperation(w, r, operationID)
	}
}

func (s *Server) observeOperatorAgent(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, "operator:provision") {
		return
	}
	var input map[string]any
	if !decodeJSON(w, r, &input) {
		return
	}
	key, uid, ok := operatorOwner(input)
	if !ok {
		writeProblem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Request validation failed", nil)
		return
	}
	externalID := optionalString(input, "externalId")
	s.mu.Lock()
	agent := s.operatorAgents[operatorOwnerIdentity(key, uid)]
	if agent == nil || (externalID != "" && externalID != agent.externalID) {
		s.mu.Unlock()
		writeProblem(w, r, http.StatusConflict, "operator_ownership_conflict", "Operator ownership conflict", nil)
		return
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"externalId": agent.externalID, "credentialGeneration": agent.credentialGeneration, "presentedCredentialGeneration": agent.credentialGeneration})
}

func (s *Server) serveScenario(w http.ResponseWriter, r *http.Request) bool {
	scenario := r.Header.Get("X-Xisnove-Mock-Scenario")
	if scenario == "" {
		return false
	}
	type failure struct {
		status int
		code   string
		title  string
	}
	failures := map[string]failure{
		"validation":   {http.StatusUnprocessableEntity, "mock_validation", "Mock validation failure"},
		"unauthorized": {http.StatusUnauthorized, "unauthorized", "Authentication required"},
		"forbidden":    {http.StatusForbidden, "insufficient_scope", "Insufficient scope"},
		"not-found":    {http.StatusNotFound, "not_found", "Resource not found"},
		"conflict":     {http.StatusConflict, "mock_conflict", "Mock conflict"},
		"rate-limit":   {http.StatusTooManyRequests, "mock_rate_limit", "Mock rate limit"},
		"server-error": {http.StatusServiceUnavailable, "mock_unavailable", "Mock service unavailable"},
	}
	selected, ok := failures[scenario]
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, "unknown_mock_scenario", "Unknown mock scenario", nil)
		return true
	}
	if scenario == "rate-limit" {
		w.Header().Set("Retry-After", "60")
	}
	var fields []FieldError
	if scenario == "validation" {
		fields = []FieldError{{Field: "body.name", Message: "is reserved by the mock scenario"}}
	}
	writeProblem(w, r, selected.status, selected.code, selected.title, fields)
	return true
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, scope string) bool {
	token := bearerToken(r)
	if token == "" {
		writeProblem(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if token == FixtureSessionToken && s.sessionActive {
		return true
	}
	if strings.HasPrefix(scope, "agent:") {
		credential := s.agentCredentialsByToken[token]
		if credential != nil && !credential.revoked {
			return true
		}
	}
	record := s.tokensByValue[token]
	if record == nil || record.RevokedAt != "" {
		writeProblem(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return false
	}
	for _, granted := range record.Scopes {
		if granted == scope {
			return true
		}
	}
	writeProblem(w, r, http.StatusForbidden, "insufficient_scope", "Insufficient scope", nil)
	return false
}

func (s *Server) authorizeSession(w http.ResponseWriter, r *http.Request) bool {
	token := bearerToken(r)
	s.mu.Lock()
	active := token == FixtureSessionToken && s.sessionActive
	s.mu.Unlock()
	if !active {
		writeProblem(w, r, http.StatusUnauthorized, "unauthorized", "Administrator session required", nil)
	}
	return active
}

func bearerToken(r *http.Request) string {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return ""
	}
	return fields[1]
}

func (s *Server) replay(w http.ResponseWriter, r *http.Request) bool {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		return false
	}
	s.mu.Lock()
	response, ok := s.idempotency[idempotencyMapKey(r, key)]
	s.mu.Unlock()
	if !ok {
		return false
	}
	captured, _ := r.Context().Value(requestHashContextKey{}).(capturedRequest)
	requestHash := captured.hash
	if response.requestHash != requestHash {
		writeProblem(w, r, http.StatusConflict, "idempotency_key_reused", "Idempotency key reused", nil)
		return true
	}
	if response.credential {
		writeProblem(w, r, http.StatusConflict, "credential_already_issued", "Credential already issued", nil)
		return true
	}
	writeStored(w, response)
	return true
}

func (s *Server) writeMutation(w http.ResponseWriter, r *http.Request, status int, value any) {
	s.writeStoredMutation(w, r, status, value, false)
}

func (s *Server) writeCredentialMutation(w http.ResponseWriter, r *http.Request, status int, value any) {
	s.writeStoredMutation(w, r, status, value, true)
}

func (s *Server) writeEmptyMutation(w http.ResponseWriter, r *http.Request, status int) {
	captured, _ := r.Context().Value(requestHashContextKey{}).(capturedRequest)
	response := storedResponse{status: status, requestHash: captured.hash}
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		s.mu.Lock()
		s.idempotency[idempotencyMapKey(r, key)] = response
		s.mu.Unlock()
	}
	writeStored(w, response)
}

func (s *Server) writeStoredMutation(w http.ResponseWriter, r *http.Request, status int, value any, credential bool) {
	body, err := json.Marshal(value)
	if err != nil {
		writeProblem(w, r, http.StatusInternalServerError, "mock_encoding_failed", "Mock encoding failed", nil)
		return
	}
	captured, _ := r.Context().Value(requestHashContextKey{}).(capturedRequest)
	requestHash := captured.hash
	response := storedResponse{
		status: status, contentType: "application/json", body: body,
		requestHash: requestHash, credential: credential,
	}
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		s.mu.Lock()
		stored := response
		if credential {
			stored.body = nil
		}
		s.idempotency[idempotencyMapKey(r, key)] = stored
		s.mu.Unlock()
	}
	writeStored(w, response)
}

func idempotencyMapKey(r *http.Request, key string) string {
	return bearerToken(r) + " " + r.Method + " " + r.URL.Path + " " + key
}

func (s *Server) lockIdempotency(r *http.Request) func() {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		return func() {}
	}
	mapKey := idempotencyMapKey(r, key)
	s.mu.Lock()
	lock := s.idempotencyLocks[mapKey]
	if lock == nil {
		lock = &sync.Mutex{}
		s.idempotencyLocks[mapKey] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func writeStored(w http.ResponseWriter, response storedResponse) {
	if response.contentType != "" {
		w.Header().Set("Content-Type", response.contentType)
	}
	w.WriteHeader(response.status)
	_, _ = w.Write(response.body)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProblem(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	code, title string,
	fields []FieldError,
) {
	correlationID := r.Header.Get("X-Request-ID")
	if correlationID == "" {
		correlationID = "mock-request-0001"
	}
	instance := r.URL.RequestURI()
	problem := Problem{
		Type: "https://xisnove.dev/problems/" + code, Title: title,
		Status: int32(status), Code: code, CorrelationId: correlationID,
		Instance: &instance,
	}
	if len(fields) > 0 {
		problem.FieldErrors = &fields
	}
	w.Header().Set("Content-Type", "application/problem+json")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeProblem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Request validation failed", []FieldError{
			{Field: "body", Message: "must match the API contract"},
		})
		return false
	}
	return true
}

func pageBounds(r *http.Request, length int) (int, int, error) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, fmt.Errorf("limit must be between 1 and 100")
		}
		limit = parsed
	}
	start := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return 0, 0, fmt.Errorf("cursor is invalid")
		}
		parsed, err := strconv.Atoi(string(decoded))
		if err != nil || parsed < 0 || parsed > length {
			return 0, 0, fmt.Errorf("cursor is invalid")
		}
		start = parsed
	}
	end := min(start+limit, length)
	return start, end, nil
}

func nextCursor(end, length int) string {
	if end >= length {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
}

func pageEnvelope(items any, next string) map[string]any {
	page := map[string]any{}
	if next != "" {
		page["nextCursor"] = next
	}
	return map[string]any{"items": items, "page": page}
}

func deterministicID(kind string, sequence int) string {
	group := map[string]string{
		"token": "4100", "monitor": "4200", "candidate": "4400", "channel": "4500",
	}[kind]
	return fmt.Sprintf("00000000-0000-%s-8000-%012d", group, sequence)
}

func cloneMap(value map[string]any) map[string]any {
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func (s *Server) operatorMutation(w http.ResponseWriter, r *http.Request, operationID string) {
	if !s.authorize(w, r, "operator:provision") {
		return
	}
	if s.replay(w, r) {
		return
	}
	var input map[string]any
	if !decodeJSON(w, r, &input) {
		return
	}
	ownerKey, ownerUID, ok := operatorOwner(input)
	if !ok {
		writeProblem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Request validation failed", nil)
		return
	}
	switch operationID {
	case "ApplyOperatorMonitor":
		s.applyOperatorMonitor(w, r, ownerKey, ownerUID)
	case "DeleteOperatorMonitor":
		s.deleteOperatorMonitor(w, r, ownerKey, ownerUID, optionalString(input, "externalId"))
	case "ApplyOperatorAgent":
		credential := nestedString(input, "initialCredential", "credential")
		generation, validGeneration := nestedInt64(input, "initialCredential", "generation")
		if credential != "" && (!validGeneration || generation != 1) {
			writeProblem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Request validation failed", nil)
			return
		}
		if credential == "" {
			generation = 1
		}
		s.applyOperatorAgent(w, r, ownerKey, ownerUID, generation, credential)
	case "PutOperatorAgentCredential":
		s.putOperatorAgentCredential(w, r, ownerKey, ownerUID, nestedGeneration(r), optionalString(input, "credential"))
	case "RevokeOperatorAgentCredential":
		s.revokeOperatorAgentCredential(w, r, ownerKey, ownerUID, nestedGeneration(r))
	case "DeleteOperatorAgent":
		s.deleteOperatorAgent(w, r, ownerKey, ownerUID, optionalString(input, "externalId"))
	}
}

func (s *Server) applyOperatorMonitor(w http.ResponseWriter, r *http.Request, key, uid string) {
	identity := operatorOwnerIdentity(key, uid)
	s.mu.Lock()
	binding, found := s.operatorMonitors[identity]
	if !found {
		s.counters["monitor"]++
		binding = mockOperatorBinding{ownerKey: key, ownerUID: uid, externalID: deterministicID("monitor", s.counters["monitor"])}
		s.operatorMonitors[identity] = binding
	}
	s.mu.Unlock()
	s.writeMutation(w, r, http.StatusOK, map[string]any{
		"externalId": binding.externalID, "state": "pending",
		"lastTransitionAt": fixtureTime, "lastObservedAt": fixtureTime,
	})
}

func (s *Server) deleteOperatorMonitor(w http.ResponseWriter, r *http.Request, key, uid, externalID string) {
	identity := operatorOwnerIdentity(key, uid)
	s.mu.Lock()
	binding, found := s.operatorMonitors[identity]
	if (!found && s.monitorBindingForExternalID(externalID)) || (found && externalID != "" && binding.externalID != externalID) {
		s.mu.Unlock()
		writeProblem(w, r, http.StatusConflict, "operator_ownership_conflict", "Operator ownership conflict", nil)
		return
	}
	delete(s.operatorMonitors, identity)
	s.mu.Unlock()
	s.writeEmptyMutation(w, r, http.StatusNoContent)
}

func (s *Server) applyOperatorAgent(w http.ResponseWriter, r *http.Request, key, uid string, generation int64, credential string) {
	identity := operatorOwnerIdentity(key, uid)
	hash := mockCredentialHash(credential)
	s.mu.Lock()
	agent, found := s.operatorAgents[identity]
	if found {
		if credential != "" && (agent.credentialGeneration != generation || agent.credentialHashes[generation] != hash) {
			s.mu.Unlock()
			writeProblem(w, r, http.StatusConflict, "operator_credential_hash_conflict", "Operator credential hash conflict", nil)
			return
		}
	} else {
		if credential == "" {
			s.mu.Unlock()
			writeProblem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Request validation failed", nil)
			return
		}
		s.counters["agent"]++
		agent = &mockOperatorAgent{
			mockOperatorBinding:  mockOperatorBinding{ownerKey: key, ownerUID: uid, externalID: operatorAgentID(s.counters["agent"])},
			credentialGeneration: generation, credentialHashes: map[int64]string{generation: hash},
		}
		s.operatorAgents[identity] = agent
	}
	s.mu.Unlock()
	s.writeMutation(w, r, http.StatusOK, map[string]any{
		"externalId": agent.externalID, "credentialGeneration": agent.credentialGeneration,
	})
}

func (s *Server) putOperatorAgentCredential(w http.ResponseWriter, r *http.Request, key, uid string, generation int64, credential string) {
	if generation < 2 || credential == "" {
		writeProblem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Request validation failed", nil)
		return
	}
	s.mu.Lock()
	agent := s.agentBindingForExternalID(r.PathValue("agentId"))
	if agent == nil || agent.ownerKey != key || agent.ownerUID != uid {
		s.mu.Unlock()
		writeProblem(w, r, http.StatusConflict, "operator_ownership_conflict", "Operator ownership conflict", nil)
		return
	}
	hash := mockCredentialHash(credential)
	if existing, found := agent.credentialHashes[generation]; found && existing != hash {
		s.mu.Unlock()
		writeProblem(w, r, http.StatusConflict, "operator_credential_hash_conflict", "Operator credential hash conflict", nil)
		return
	}
	agent.credentialHashes[generation] = hash
	if generation > agent.credentialGeneration {
		agent.credentialGeneration = generation
	}
	s.mu.Unlock()
	s.writeEmptyMutation(w, r, http.StatusNoContent)
}

func (s *Server) revokeOperatorAgentCredential(w http.ResponseWriter, r *http.Request, key, uid string, generation int64) {
	s.mu.Lock()
	agent := s.agentBindingForExternalID(r.PathValue("agentId"))
	if agent == nil || agent.ownerKey != key || agent.ownerUID != uid {
		s.mu.Unlock()
		writeProblem(w, r, http.StatusConflict, "operator_ownership_conflict", "Operator ownership conflict", nil)
		return
	}
	if generation >= agent.credentialGeneration {
		s.mu.Unlock()
		writeProblem(w, r, http.StatusConflict, "credential_overlap_incomplete", "Replacement credential has not completed an authenticated heartbeat", nil)
		return
	}
	delete(agent.credentialHashes, generation)
	s.mu.Unlock()
	s.writeEmptyMutation(w, r, http.StatusNoContent)
}

func (s *Server) deleteOperatorAgent(w http.ResponseWriter, r *http.Request, key, uid, externalID string) {
	identity := operatorOwnerIdentity(key, uid)
	s.mu.Lock()
	agent, found := s.operatorAgents[identity]
	if (!found && s.agentBindingForExternalID(externalID) != nil) || (found && externalID != "" && agent.externalID != externalID) {
		s.mu.Unlock()
		writeProblem(w, r, http.StatusConflict, "operator_ownership_conflict", "Operator ownership conflict", nil)
		return
	}
	delete(s.operatorAgents, identity)
	s.mu.Unlock()
	s.writeEmptyMutation(w, r, http.StatusNoContent)
}

func (s *Server) upsertCompleteDiscoveryCandidates(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, "agent:discovery") {
		return
	}
	if s.replay(w, r) {
		return
	}
	var input struct {
		Candidates  []map[string]any `json:"candidates"`
		Complete    bool             `json:"complete"`
		CompletedAt string           `json:"completedAt"`
	}
	if !decodeJSON(w, r, &input) || len(input.Candidates) > 500 {
		if len(input.Candidates) > 500 {
			writeProblem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Request validation failed", nil)
		}
		return
	}
	s.mu.Lock()
	created, updated := 0, 0
	for _, candidateInput := range input.Candidates {
		index := -1
		for i, candidate := range s.candidates {
			if candidate["agentId"] == fixtureAgentID && candidate["locationId"] == fixtureLocationID &&
				candidate["sourceKind"] == candidateInput["sourceKind"] && candidate["sourceUid"] == candidateInput["sourceUid"] &&
				candidate["protocol"] == candidateInput["protocol"] && candidate["target"] == candidateInput["target"] {
				index = i
				break
			}
		}
		present, _ := candidateInput["present"].(bool)
		if index < 0 && !present {
			continue
		}
		if index < 0 {
			s.counters["candidate"]++
			s.candidates = append(s.candidates, map[string]any{
				"id": deterministicID("candidate", s.counters["candidate"]), "agentId": fixtureAgentID, "locationId": fixtureLocationID,
				"sourceKind": candidateInput["sourceKind"], "sourceUid": candidateInput["sourceUid"], "namespace": candidateInput["namespace"],
				"name": candidateInput["name"], "labels": candidateInput["labels"], "protocol": candidateInput["protocol"],
				"target": candidateInput["target"], "networkPerspective": candidateInput["networkPerspective"], "present": true,
				"state": "pending", "firstSeenAt": candidateInput["observedAt"], "lastObservedAt": candidateInput["observedAt"], "updatedAt": candidateInput["observedAt"],
			})
			created++
			continue
		}
		for _, field := range []string{"namespace", "name", "labels", "networkPerspective", "present"} {
			s.candidates[index][field] = candidateInput[field]
		}
		s.candidates[index]["lastObservedAt"] = candidateInput["observedAt"]
		s.candidates[index]["updatedAt"] = candidateInput["observedAt"]
		updated++
	}
	if input.Complete {
		for _, candidate := range s.candidates {
			candidate["present"] = false
		}
		for _, candidateInput := range input.Candidates {
			for _, candidate := range s.candidates {
				if candidate["sourceKind"] == candidateInput["sourceKind"] && candidate["sourceUid"] == candidateInput["sourceUid"] &&
					candidate["protocol"] == candidateInput["protocol"] && candidate["target"] == candidateInput["target"] {
					candidate["present"] = candidateInput["present"]
				}
			}
		}
	}
	s.mu.Unlock()
	s.writeMutation(w, r, http.StatusOK, map[string]any{"accepted": len(input.Candidates), "created": created, "updated": updated})
}

func operatorOwner(input map[string]any) (string, string, bool) {
	owner, ok := input["owner"].(map[string]any)
	if !ok {
		return "", "", false
	}
	key, keyOK := owner["key"].(string)
	uid, uidOK := owner["uid"].(string)
	return key, uid, keyOK && uidOK && key != "" && uid != ""
}

func operatorOwnerIdentity(key, uid string) string { return key + "\x00" + uid }

func optionalString(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return value
}

func nestedString(input map[string]any, parent, key string) string {
	value, _ := input[parent].(map[string]any)
	return optionalString(value, key)
}

func nestedInt64(input map[string]any, parent, key string) (int64, bool) {
	value, _ := input[parent].(map[string]any)
	switch number := value[key].(type) {
	case float64:
		return int64(number), number >= 1 && number == float64(int64(number))
	case json.Number:
		parsed, err := number.Int64()
		return parsed, err == nil && parsed >= 1
	default:
		return 0, false
	}
}

func nestedGeneration(r *http.Request) int64 {
	generation, err := strconv.ParseInt(r.PathValue("generation"), 10, 64)
	if err != nil || generation < 1 {
		return 0
	}
	return generation
}

func mockCredentialHash(credential string) string {
	sum := sha256.Sum256([]byte(credential))
	return fmt.Sprintf("%x", sum[:])
}

func operatorAgentID(sequence int) string {
	return fmt.Sprintf("00000000-0000-4800-8000-%012d", sequence)
}

func (s *Server) monitorBindingForExternalID(externalID string) bool {
	if externalID == "" {
		return false
	}
	for _, binding := range s.operatorMonitors {
		if binding.externalID == externalID {
			return true
		}
	}
	return false
}

func (s *Server) agentBindingForExternalID(externalID string) *mockOperatorAgent {
	for _, agent := range s.operatorAgents {
		if agent.externalID == externalID {
			return agent
		}
	}
	return nil
}
