package mockapi

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
)

func (s *Server) seedFixtures() {
	full := &apiTokenRecord{
		ID: "00000000-0000-4100-8000-000000000001", Token: FixtureFullAPIToken,
		Name: "fixture full access",
		Scopes: []string{
			"tokens:read", "tokens:write", "locations:read", "locations:write",
			"monitors:read", "monitors:write", "agents:read", "agents:write", "incidents:read",
			"notifications:read", "notifications:write",
			"maintenance:read", "maintenance:write", "discovery:read", "discovery:write", "status:read",
		},
	}
	readOnly := &apiTokenRecord{
		ID: "00000000-0000-4100-8000-000000000002", Token: FixtureReadOnlyAPIToken,
		Name: "fixture read only",
		Scopes: []string{
			"tokens:read", "locations:read", "monitors:read", "agents:read",
			"incidents:read", "notifications:read", "maintenance:read", "discovery:read",
		},
	}
	for _, record := range []*apiTokenRecord{full, readOnly} {
		s.tokensByID[record.ID] = record
		s.tokensByValue[record.Token] = record
	}

	routerID := "00000000-0000-4200-8000-000000000101"
	dnsID := "00000000-0000-4200-8000-000000000102"
	s.monitors = []map[string]any{
		monitorFixture(
			routerID,
			"homelab router",
			"Public edge availability",
			true,
			map[string]any{
				"kind": "http", "method": "GET", "url": "https://router.example.test/health",
				"headers": map[string]string{}, "body": "",
				"expectedStatus": []map[string]int{{"minimum": 200, "maximum": 299}},
				"bodyContains":   []string{"ok"}, "bodyDoesNotContain": []string{},
				"followRedirects": false,
			},
		),
		monitorFixture(
			dnsID,
			"authoritative DNS",
			"Private DNS fixture",
			false,
			map[string]any{
				"kind": "dns", "resolver": "1.1.1.1:53", "name": "example.test",
				"recordType": "A", "expectedValues": []string{"192.0.2.10"},
			},
		),
	}

	incidentID := "00000000-0000-4300-8000-000000000201"
	s.incidents = []map[string]any{{
		"id": incidentID, "monitorId": routerID, "state": "open", "severity": "critical",
		"openedAt": fixtureTime, "lastTransitionAt": fixtureTime,
	}}
	s.events[incidentID] = []map[string]any{{
		"id": "00000000-0000-4300-8000-000000000301", "incidentId": incidentID,
		"action": "open", "previousState": "up", "state": "down",
		"severity": "critical", "occurredAt": fixtureTime,
	}}

	s.candidates = []map[string]any{{
		"id":      "00000000-0000-4400-8000-000000000401",
		"agentId": "00000000-0000-4800-8000-000000000801", "locationId": fixtureLocationID,
		"sourceKind": "service", "sourceUid": "service/monitoring/router", "namespace": "monitoring",
		"name": "router metrics", "labels": map[string]string{"namespace": "monitoring"},
		"protocol": "http", "target": "https://router.example.test/metrics",
		"networkPerspective": "cluster/default", "present": true, "state": "pending",
		"firstSeenAt": fixtureTime, "lastObservedAt": fixtureTime, "updatedAt": fixtureTime,
	}}

	channelID := "00000000-0000-4500-8000-000000000501"
	routeID := "00000000-0000-4600-8000-000000000601"
	s.channels = []map[string]any{{
		"id": channelID, "name": "fixture Alertmanager", "kind": "alertmanager",
		"enabled": true, "createdAt": fixtureTime, "updatedAt": fixtureTime,
	}}
	s.routes = []map[string]any{{
		"id": routeID, "name": "critical incidents", "channelId": channelID,
		"labelMatchers": map[string]string{}, "actions": []string{"open", "change", "recover"},
		"severities": []string{"critical"}, "template": "{{ .MonitorName }} is {{ .State }}",
		"enabled": true, "precedence": 10, "createdAt": fixtureTime, "updatedAt": fixtureTime,
	}}
	s.deliveries = []map[string]any{{
		"id": "00000000-0000-4700-8000-000000000701", "routeId": routeID,
		"channelId": channelID, "state": "delivered", "availableAt": fixtureTime,
		"attemptCount": 1, "deliveredAt": fixtureTime,
		"renderSnapshot": map[string]any{
			"eventId": s.events[incidentID][0]["id"], "action": "open",
			"incidentId": incidentID, "monitorId": routerID, "monitorName": "homelab router",
			"monitorDescription": "Public edge availability", "monitorLabels": map[string]string{"site": "home"},
			"previousState": "up", "state": "down", "severity": "critical", "occurredAt": fixtureTime,
			"routeId": routeID, "channelId": channelID, "channelKind": "alertmanager",
			"template": "{{ .MonitorName }} is {{ .State }}", "routeUpdatedAt": fixtureTime,
		},
		"createdAt": fixtureTime, "updatedAt": fixtureTime,
	}}
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Email != FixtureAdminEmail || input.Password != FixtureAdminPassword {
		writeProblem(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials", nil)
		return
	}
	s.mu.Lock()
	s.sessionActive = true
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": FixtureSessionToken, "expiresAt": "2026-07-26T00:00:00Z",
	})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeSession(w, r) {
		return
	}
	s.mu.Lock()
	s.sessionActive = false
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiTokens(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeSession(w, r) {
		return
	}
	switch r.Method {
	case http.MethodPost:
		if s.replay(w, r) {
			return
		}
		var input struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.Name == "" || len(input.Scopes) == 0 {
			writeProblem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Request validation failed", nil)
			return
		}
		s.mu.Lock()
		s.counters["token"]++
		sequence := s.counters["token"]
		record := &apiTokenRecord{
			ID:    deterministicID("token", sequence),
			Token: fmt.Sprintf("xisnove_mock_api_generated_%024d", sequence),
			Name:  input.Name, Scopes: slices.Clone(input.Scopes),
		}
		s.tokensByID[record.ID] = record
		s.tokensByValue[record.Token] = record
		s.mu.Unlock()
		s.writeCredentialMutation(w, r, http.StatusCreated, map[string]any{
			"apiToken": tokenView(record), "token": record.Token,
		})
	case http.MethodGet:
		s.mu.Lock()
		items := make([]map[string]any, 0, len(s.tokensByID))
		for _, record := range s.tokensByID {
			items = append(items, tokenView(record))
		}
		s.mu.Unlock()
		slices.SortFunc(items, func(a, b map[string]any) int {
			return strings.Compare(a["id"].(string), b["id"].(string))
		})
		writeJSON(w, http.StatusOK, pageEnvelope(items, ""))
	default:
		writeProblem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
	}
}

func (s *Server) apiToken(w http.ResponseWriter, r *http.Request, tokenID string) {
	if !s.authorizeSession(w, r) {
		return
	}
	s.mu.Lock()
	record := s.tokensByID[tokenID]
	s.mu.Unlock()
	if record == nil {
		writeProblem(w, r, http.StatusNotFound, "api_token_not_found", "API token not found", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, tokenView(record))
	case http.MethodPatch:
		if s.replay(w, r) {
			return
		}
		var input struct {
			Name   *string  `json:"name"`
			Scopes []string `json:"scopes"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		s.mu.Lock()
		if input.Name != nil {
			record.Name = *input.Name
		}
		if input.Scopes != nil {
			record.Scopes = slices.Clone(input.Scopes)
		}
		view := tokenView(record)
		s.mu.Unlock()
		s.writeMutation(w, r, http.StatusOK, view)
	case http.MethodDelete:
		s.mu.Lock()
		record.RevokedAt = fixtureTime
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		writeProblem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
	}
}

func tokenView(record *apiTokenRecord) map[string]any {
	view := map[string]any{
		"id": record.ID, "name": record.Name, "scopes": slices.Clone(record.Scopes),
		"createdAt": fixtureTime, "updatedAt": fixtureTime,
	}
	if record.RevokedAt != "" {
		view["revokedAt"] = record.RevokedAt
	}
	return view
}

func (s *Server) monitorCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !s.authorize(w, r, "monitors:read") {
			return
		}
		s.mu.Lock()
		items := slices.Clone(s.monitors)
		s.mu.Unlock()
		start, end, err := pageBounds(r, len(items))
		if err != nil {
			writeProblem(w, r, http.StatusBadRequest, "invalid_cursor", "Invalid page", nil)
			return
		}
		writeJSON(w, http.StatusOK, pageEnvelope(items[start:end], nextCursor(end, len(items))))
	case http.MethodPost:
		if !s.authorize(w, r, "monitors:write") {
			return
		}
		if s.replay(w, r) {
			return
		}
		var input map[string]any
		if !decodeJSON(w, r, &input) {
			return
		}
		name, _ := input["name"].(string)
		if name == "" {
			writeProblem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Request validation failed", nil)
			return
		}
		s.mu.Lock()
		s.counters["monitor"]++
		monitor := monitorFromInput(deterministicID("monitor", s.counters["monitor"]), input)
		s.monitors = append(s.monitors, monitor)
		s.mu.Unlock()
		s.writeMutation(w, r, http.StatusCreated, monitor)
	default:
		writeProblem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
	}
}

func (s *Server) monitor(w http.ResponseWriter, r *http.Request, monitorID string) {
	scope := "monitors:read"
	if r.Method == http.MethodPut || r.Method == http.MethodDelete {
		scope = "monitors:write"
	}
	if !s.authorize(w, r, scope) {
		return
	}
	s.mu.Lock()
	index := slices.IndexFunc(s.monitors, func(item map[string]any) bool {
		return item["id"] == monitorID
	})
	if index < 0 {
		s.mu.Unlock()
		writeProblem(w, r, http.StatusNotFound, "monitor_not_found", "Monitor not found", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		item := cloneMap(s.monitors[index])
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		s.mu.Unlock()
		if s.replay(w, r) {
			return
		}
		var input map[string]any
		if !decodeJSON(w, r, &input) {
			return
		}
		s.mu.Lock()
		for key, value := range input {
			s.monitors[index][key] = value
		}
		s.monitors[index]["updatedAt"] = fixtureTime
		item := cloneMap(s.monitors[index])
		s.mu.Unlock()
		s.writeMutation(w, r, http.StatusOK, item)
	case http.MethodDelete:
		s.monitors[index]["enabled"] = false
		s.monitors[index]["updatedAt"] = fixtureTime
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		s.mu.Unlock()
		writeProblem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
	}
}

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, "incidents:read") {
		return
	}
	s.mu.Lock()
	items := slices.Clone(s.incidents)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, pageEnvelope(items, ""))
}

func (s *Server) incident(w http.ResponseWriter, r *http.Request, remainder string) {
	if !s.authorize(w, r, "incidents:read") {
		return
	}
	if r.Method != http.MethodGet {
		writeProblem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
		return
	}
	if strings.HasSuffix(remainder, "/events") {
		incidentID := strings.TrimSuffix(remainder, "/events")
		s.mu.Lock()
		items, found := s.events[incidentID]
		s.mu.Unlock()
		if !found {
			writeProblem(w, r, http.StatusNotFound, "incident_not_found", "Incident not found", nil)
			return
		}
		writeJSON(w, http.StatusOK, pageEnvelope(items, ""))
		return
	}
	s.mu.Lock()
	index := slices.IndexFunc(s.incidents, func(item map[string]any) bool {
		return item["id"] == remainder
	})
	if index >= 0 {
		item := cloneMap(s.incidents[index])
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, item)
		return
	}
	s.mu.Unlock()
	writeProblem(w, r, http.StatusNotFound, "incident_not_found", "Incident not found", nil)
}

func (s *Server) upsertDiscoveryCandidates(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, "agent:discovery") {
		return
	}
	if s.replay(w, r) {
		return
	}
	var input struct {
		Candidates []map[string]any `json:"candidates"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Candidates) < 1 || len(input.Candidates) > 500 {
		writeProblem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Request validation failed", nil)
		return
	}
	s.mu.Lock()
	created, updated := 0, 0
	for _, candidateInput := range input.Candidates {
		index := slices.IndexFunc(s.candidates, func(item map[string]any) bool {
			return item["agentId"] == "00000000-0000-4800-8000-000000000801" &&
				item["locationId"] == fixtureLocationID &&
				item["sourceKind"] == candidateInput["sourceKind"] &&
				item["sourceUid"] == candidateInput["sourceUid"] &&
				item["protocol"] == candidateInput["protocol"] &&
				item["target"] == candidateInput["target"]
		})
		present, _ := candidateInput["present"].(bool)
		if index < 0 && !present {
			continue
		}
		if index < 0 {
			s.counters["candidate"]++
			s.candidates = append(s.candidates, map[string]any{
				"id":      deterministicID("candidate", s.counters["candidate"]),
				"agentId": "00000000-0000-4800-8000-000000000801", "locationId": fixtureLocationID,
				"sourceKind": candidateInput["sourceKind"], "sourceUid": candidateInput["sourceUid"],
				"namespace": candidateInput["namespace"], "name": candidateInput["name"],
				"labels": candidateInput["labels"], "protocol": candidateInput["protocol"],
				"target": candidateInput["target"], "networkPerspective": candidateInput["networkPerspective"],
				"present": true, "state": "pending", "firstSeenAt": candidateInput["observedAt"],
				"lastObservedAt": candidateInput["observedAt"], "updatedAt": candidateInput["observedAt"],
			})
			created++
			continue
		}
		observedAt, _ := candidateInput["observedAt"].(string)
		lastObservedAt, _ := s.candidates[index]["lastObservedAt"].(string)
		if observedAt < lastObservedAt {
			continue
		}
		for _, field := range []string{
			"namespace", "name", "labels", "networkPerspective", "present",
		} {
			s.candidates[index][field] = candidateInput[field]
		}
		s.candidates[index]["lastObservedAt"] = candidateInput["observedAt"]
		s.candidates[index]["updatedAt"] = candidateInput["observedAt"]
		updated++
	}
	count := len(input.Candidates)
	s.mu.Unlock()
	s.writeMutation(w, r, http.StatusOK, map[string]any{
		"accepted": count, "created": created, "updated": updated,
	})
}

func (s *Server) listDiscoveryCandidates(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, "discovery:read") {
		return
	}
	s.mu.Lock()
	items := slices.Clone(s.candidates)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, pageEnvelope(items, ""))
}

func (s *Server) discoveryCandidate(w http.ResponseWriter, r *http.Request, remainder string) {
	if strings.HasSuffix(remainder, "/promotion") && r.Method == http.MethodPost {
		if !s.authorize(w, r, "discovery:write") {
			return
		}
		if s.replay(w, r) {
			return
		}
		candidateID := strings.TrimSuffix(remainder, "/promotion")
		var input map[string]any
		if !decodeJSON(w, r, &input) {
			return
		}
		s.mu.Lock()
		index := slices.IndexFunc(s.candidates, func(item map[string]any) bool {
			return item["id"] == candidateID
		})
		if index < 0 {
			s.mu.Unlock()
			writeProblem(w, r, http.StatusNotFound, "discovery_candidate_not_found", "Discovery candidate not found", nil)
			return
		}
		s.counters["monitor"]++
		target, _ := s.candidates[index]["target"].(string)
		input["probe"] = map[string]any{
			"kind": "http", "method": "GET", "url": target,
			"headers": map[string]string{}, "body": "",
			"expectedStatus": []map[string]int{{"minimum": 200, "maximum": 299}},
			"bodyContains":   []string{}, "bodyDoesNotContain": []string{},
			"followRedirects": false,
		}
		monitor := monitorFromInput(deterministicID("monitor", s.counters["monitor"]), input)
		s.monitors = append(s.monitors, monitor)
		s.candidates[index]["state"] = "promoted"
		s.candidates[index]["promotedMonitorId"] = monitor["id"]
		candidate := cloneMap(s.candidates[index])
		s.mu.Unlock()
		s.writeMutation(w, r, http.StatusCreated, map[string]any{
			"candidate": candidate, "monitor": monitor,
		})
		return
	}
	if r.Method != http.MethodGet {
		writeProblem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
		return
	}
	if !s.authorize(w, r, "discovery:read") {
		return
	}
	s.mu.Lock()
	index := slices.IndexFunc(s.candidates, func(item map[string]any) bool {
		return item["id"] == remainder
	})
	if index >= 0 {
		item := cloneMap(s.candidates[index])
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, item)
		return
	}
	s.mu.Unlock()
	writeProblem(w, r, http.StatusNotFound, "discovery_candidate_not_found", "Discovery candidate not found", nil)
}

func (s *Server) notificationChannels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !s.authorize(w, r, "notifications:read") {
			return
		}
		s.mu.Lock()
		items := slices.Clone(s.channels)
		s.mu.Unlock()
		page := pageEnvelope(items, "")
		page["limit"], page["offset"] = 50, 0
		writeJSON(w, http.StatusOK, page)
	case http.MethodPost:
		if !s.authorize(w, r, "notifications:write") {
			return
		}
		if s.replay(w, r) {
			return
		}
		var input struct {
			Name          string         `json:"name"`
			Enabled       bool           `json:"enabled"`
			Configuration map[string]any `json:"configuration"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		kind, _ := input.Configuration["kind"].(string)
		s.mu.Lock()
		s.counters["channel"]++
		channel := map[string]any{
			"id":   deterministicID("channel", s.counters["channel"]),
			"name": input.Name, "kind": kind, "enabled": input.Enabled,
			"createdAt": fixtureTime, "updatedAt": fixtureTime,
		}
		s.channels = append(s.channels, channel)
		s.mu.Unlock()
		s.writeMutation(w, r, http.StatusCreated, channel)
	default:
		writeProblem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
	}
}

func (s *Server) listNotificationRoutes(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, "notifications:read") {
		return
	}
	s.mu.Lock()
	items := slices.Clone(s.routes)
	s.mu.Unlock()
	page := pageEnvelope(items, "")
	page["limit"], page["offset"] = 50, 0
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) listNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, "notifications:read") {
		return
	}
	s.mu.Lock()
	items := slices.Clone(s.deliveries)
	s.mu.Unlock()
	page := pageEnvelope(items, "")
	page["limit"], page["offset"] = 50, 0
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) publicStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	publicMonitors := make([]map[string]any, 0)
	for _, monitor := range s.monitors {
		if monitor["public"] != true || monitor["enabled"] != true {
			continue
		}
		state := "up"
		if monitor["id"] == "00000000-0000-4200-8000-000000000101" {
			state = "down"
		}
		publicMonitors = append(publicMonitors, map[string]any{
			"id": monitor["id"], "name": monitor["name"], "description": monitor["description"],
			"state": state, "recentUptime": []map[string]any{
				{"date": "2026-07-25", "uptimePercentage": 99.5},
			},
		})
	}
	active := make([]map[string]any, 0, len(s.incidents))
	for _, incident := range s.incidents {
		if incident["state"] != "open" {
			continue
		}
		active = append(active, map[string]any{
			"id": incident["id"], "monitorId": incident["monitorId"], "monitorName": "homelab router",
			"state": "open", "severity": incident["severity"], "openedAt": incident["openedAt"],
			"lastTransitionAt": incident["lastTransitionAt"],
		})
	}
	s.mu.Unlock()
	state := "up"
	if len(active) > 0 {
		state = "down"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"generatedAt": fixtureTime, "state": state,
		"monitors": publicMonitors, "activeIncidents": active,
	})
}

func monitorFixture(id, name, description string, public bool, probe map[string]any) map[string]any {
	kind, _ := probe["kind"].(string)
	return map[string]any{
		"id": id, "kind": kind, "name": name, "description": description,
		"labels": map[string]string{"site": "home"}, "displayOrder": 10,
		"public": public, "enabled": true, "intervalSeconds": 30, "timeoutMillis": 5000,
		"failureThreshold": 3, "recoveryThreshold": 2,
		"locationId": fixtureLocationID, "requiredLocation": true, "probe": probe,
		"createdAt": fixtureTime, "updatedAt": fixtureTime,
	}
}

func monitorFromInput(id string, input map[string]any) map[string]any {
	probe, _ := input["probe"].(map[string]any)
	kind, _ := probe["kind"].(string)
	value := map[string]any{
		"id": id, "kind": kind, "name": input["name"], "description": stringValue(input, "description"),
		"labels": mapValue(input, "labels"), "displayOrder": numberValue(input, "displayOrder"),
		"public": boolValue(input, "public"), "enabled": true,
		"intervalSeconds": input["intervalSeconds"], "timeoutMillis": input["timeoutMillis"],
		"failureThreshold": input["failureThreshold"], "recoveryThreshold": input["recoveryThreshold"],
		"locationId": input["locationId"], "requiredLocation": input["requiredLocation"], "probe": probe,
		"createdAt": fixtureTime, "updatedAt": fixtureTime,
	}
	return value
}

func stringValue(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return value
}

func boolValue(input map[string]any, key string) bool {
	value, _ := input[key].(bool)
	return value
}

func numberValue(input map[string]any, key string) any {
	if value, found := input[key]; found {
		return value
	}
	return 0
}

func mapValue(input map[string]any, key string) any {
	if value, found := input[key]; found {
		return value
	}
	return map[string]string{}
}
