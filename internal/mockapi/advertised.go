package mockapi

import "net/http"

const (
	fixtureAgentID     = "00000000-0000-4800-8000-000000000801"
	fixtureMaintenance = "00000000-0000-4900-8000-000000000901"
)

func (s *Server) serveAdvertisedOperation(w http.ResponseWriter, r *http.Request, operationID string) {
	scope := advertisedScope(operationID)
	if scope != "" && !s.authorize(w, r, scope) {
		return
	}

	switch operationID {
	case "CreateAgentEnrollmentToken":
		if s.replay(w, r) {
			return
		}
		s.writeCredentialMutation(w, r, http.StatusCreated, map[string]any{
			"token": "xisnove_mock_enrollment_0000000000000000000001", "expiresAt": fixtureTime,
		})
	case "EnrollAgent":
		writeJSON(w, http.StatusCreated, map[string]any{
			"agentId": fixtureAgentID, "credential": FixtureAgentToken, "credentialGeneration": 1,
		})
	case "HeartbeatAgent", "RevokeAgent", "DisableLocation", "DisableNotificationChannel",
		"DisableNotificationRoute", "DeleteMaintenance":
		w.WriteHeader(http.StatusNoContent)
	case "LeaseAgentWork":
		w.WriteHeader(http.StatusNoContent)
	case "UploadProbeResults":
		writeJSON(w, http.StatusOK, map[string]any{"acknowledgements": []any{}})
	case "ListAgents":
		writeJSON(w, http.StatusOK, pageEnvelope([]any{agentFixture()}, ""))
	case "GetAgent", "UpdateAgent":
		if operationID == "UpdateAgent" {
			if s.replay(w, r) {
				return
			}
			s.writeMutation(w, r, http.StatusOK, agentFixture())
			return
		}
		writeJSON(w, http.StatusOK, agentFixture())
	case "RotateAgentCredential":
		if s.replay(w, r) {
			return
		}
		s.writeCredentialMutation(w, r, http.StatusCreated, map[string]any{
			"agentId": fixtureAgentID, "credential": "xisnove_mock_agent_rotated_000000000000000001",
			"credentialGeneration": 2,
		})
	case "ListLocations":
		writeJSON(w, http.StatusOK, pageEnvelope([]any{locationFixture()}, ""))
	case "CreateLocation":
		if s.replay(w, r) {
			return
		}
		s.writeMutation(w, r, http.StatusCreated, locationFixture())
	case "GetLocation":
		writeJSON(w, http.StatusOK, locationFixture())
	case "UpdateLocation":
		if s.replay(w, r) {
			return
		}
		s.writeMutation(w, r, http.StatusOK, locationFixture())
	case "GetMonitorHealth":
		writeJSON(w, http.StatusOK, map[string]any{
			"monitorId": "00000000-0000-4200-8000-000000000101", "state": "down",
			"lastTransitionAt": fixtureTime, "locations": []any{},
		})
	case "GetActiveMonitorIncident":
		s.mu.Lock()
		incident := cloneMap(s.incidents[0])
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, incident)
	case "GetNotificationChannel", "UpdateNotificationChannel":
		if operationID == "UpdateNotificationChannel" {
			if s.replay(w, r) {
				return
			}
			s.writeMutation(w, r, http.StatusOK, notificationChannelFixture())
			return
		}
		writeJSON(w, http.StatusOK, notificationChannelFixture())
	case "CreateNotificationRoute":
		if s.replay(w, r) {
			return
		}
		s.writeMutation(w, r, http.StatusCreated, notificationRouteFixture())
	case "GetNotificationRoute", "UpdateNotificationRoute":
		if operationID == "UpdateNotificationRoute" {
			if s.replay(w, r) {
				return
			}
			s.writeMutation(w, r, http.StatusOK, notificationRouteFixture())
			return
		}
		writeJSON(w, http.StatusOK, notificationRouteFixture())
	case "GetNotificationDelivery":
		writeJSON(w, http.StatusOK, map[string]any{
			"delivery": notificationDeliveryFixture(), "attempts": []any{},
		})
	case "ReplayNotificationDelivery":
		if s.replay(w, r) {
			return
		}
		s.writeMutation(w, r, http.StatusAccepted, map[string]any{})
	case "ListMaintenance":
		page := pageEnvelope([]any{maintenanceFixture()}, "")
		page["limit"], page["offset"] = 50, 0
		writeJSON(w, http.StatusOK, page)
	case "CreateMaintenance":
		if s.replay(w, r) {
			return
		}
		s.writeMutation(w, r, http.StatusCreated, maintenanceFixture())
	case "GetMaintenance":
		writeJSON(w, http.StatusOK, maintenanceFixture())
	case "EndMaintenance":
		if s.replay(w, r) {
			return
		}
		s.writeMutation(w, r, http.StatusOK, maintenanceFixture())
	default:
		writeProblem(w, r, http.StatusNotFound, "not_found", "Resource not found", nil)
	}
}

func advertisedScope(operationID string) string {
	switch operationID {
	case "CreateAgentEnrollmentToken", "UpdateAgent", "RevokeAgent", "RotateAgentCredential":
		return "agents:write"
	case "ListAgents", "GetAgent":
		return "agents:read"
	case "HeartbeatAgent":
		return "agent:heartbeat"
	case "UploadProbeResults":
		return "agent:results"
	case "LeaseAgentWork":
		return "agent:work"
	case "CreateLocation", "UpdateLocation", "DisableLocation":
		return "locations:write"
	case "ListLocations", "GetLocation":
		return "locations:read"
	case "GetMonitorHealth":
		return "monitors:read"
	case "GetActiveMonitorIncident":
		return "incidents:read"
	case "CreateNotificationRoute", "UpdateNotificationChannel", "UpdateNotificationRoute",
		"DisableNotificationChannel", "DisableNotificationRoute", "ReplayNotificationDelivery":
		return "notifications:write"
	case "GetNotificationChannel", "GetNotificationRoute", "GetNotificationDelivery":
		return "notifications:read"
	case "CreateMaintenance", "EndMaintenance", "DeleteMaintenance":
		return "maintenance:write"
	case "ListMaintenance", "GetMaintenance":
		return "maintenance:read"
	default:
		return ""
	}
}

func locationFixture() map[string]any {
	return map[string]any{
		"id": fixtureLocationID, "name": "hybrid homelab", "enabled": true,
		"createdAt": fixtureTime, "updatedAt": fixtureTime,
	}
}

func agentFixture() map[string]any {
	return map[string]any{
		"id": fixtureAgentID, "name": "homelab agent", "locationId": fixtureLocationID,
		"enabled": true, "credentialGeneration": 1,
		"capabilities": []string{"http", "tcp", "dns", "kubernetes-discovery"},
		"version":      "mock", "lastSeenAt": fixtureTime, "createdAt": fixtureTime, "updatedAt": fixtureTime,
	}
}

func notificationChannelFixture() map[string]any {
	return map[string]any{
		"id": "00000000-0000-4500-8000-000000000501", "name": "fixture Alertmanager",
		"kind": "alertmanager", "enabled": true, "createdAt": fixtureTime, "updatedAt": fixtureTime,
	}
}

func notificationRouteFixture() map[string]any {
	return map[string]any{
		"id": "00000000-0000-4600-8000-000000000601", "name": "critical incidents",
		"channelId": "00000000-0000-4500-8000-000000000501", "labelMatchers": map[string]string{},
		"actions": []string{"open", "change", "recover"}, "severities": []string{"critical"},
		"template": "{{ .MonitorName }} is {{ .State }}", "enabled": true, "precedence": 10,
		"createdAt": fixtureTime, "updatedAt": fixtureTime,
	}
}

func notificationDeliveryFixture() map[string]any {
	return map[string]any{
		"id":        "00000000-0000-4700-8000-000000000701",
		"routeId":   "00000000-0000-4600-8000-000000000601",
		"channelId": "00000000-0000-4500-8000-000000000501", "state": "delivered",
		"availableAt": fixtureTime, "attemptCount": 1, "renderSnapshot": map[string]any{
			"eventId": "00000000-0000-4300-8000-000000000301", "action": "open",
			"incidentId": "00000000-0000-4300-8000-000000000201",
			"monitorId":  "00000000-0000-4200-8000-000000000101", "monitorName": "homelab router",
			"monitorDescription": "Public edge availability", "monitorLabels": map[string]string{"site": "home"},
			"previousState": "up", "state": "down", "severity": "critical", "occurredAt": fixtureTime,
			"routeId":   "00000000-0000-4600-8000-000000000601",
			"channelId": "00000000-0000-4500-8000-000000000501", "channelKind": "alertmanager",
			"template": "{{ .MonitorName }} is {{ .State }}", "routeUpdatedAt": fixtureTime,
		}, "createdAt": fixtureTime, "updatedAt": fixtureTime,
	}
}

func maintenanceFixture() map[string]any {
	return map[string]any{
		"id": fixtureMaintenance, "monitorId": "00000000-0000-4200-8000-000000000101",
		"startsAt": fixtureTime, "reason": "mock maintenance", "createdAt": fixtureTime, "updatedAt": fixtureTime,
	}
}
