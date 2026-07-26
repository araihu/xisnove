package application

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/araihu/xisnove/domain"
)

var (
	ErrInvalidScopes = errors.New("invalid API token scopes")
	ErrInvalidExpiry = errors.New("API token expiry must be in the future")
	ErrForbidden     = errors.New("forbidden")
)

var recognizedScopes = map[Scope]struct{}{
	ScopeTokensRead: {}, ScopeTokensWrite: {},
	ScopeLocationsRead: {}, ScopeLocationsWrite: {},
	ScopeMonitorsRead: {}, ScopeMonitorsWrite: {},
	ScopeAgentsRead: {}, ScopeAgentsWrite: {},
	ScopeIncidentsRead:     {},
	ScopeNotificationsRead: {}, ScopeNotificationsWrite: {},
	ScopeMaintenanceRead: {}, ScopeMaintenanceWrite: {},
	ScopeDiscoveryRead: {}, ScopeDiscoveryWrite: {},
	ScopeStatusRead: {},
}

func NormalizeScopes(scopes []Scope) ([]Scope, error) {
	if len(scopes) == 0 {
		return nil, ErrInvalidScopes
	}
	normalized := slices.Clone(scopes)
	seen := make(map[Scope]struct{}, len(normalized))
	for _, scope := range normalized {
		if _, ok := recognizedScopes[scope]; !ok {
			return nil, ErrInvalidScopes
		}
		if _, duplicate := seen[scope]; duplicate {
			return nil, ErrInvalidScopes
		}
		seen[scope] = struct{}{}
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized, nil
}

type CreateAPITokenCommand struct {
	Label     string
	Scopes    []Scope
	ExpiresAt *time.Time
}

type APITokenCredential struct {
	Token  string
	Record APITokenRecord
}

type APITokenServiceConfig struct {
	Store  UnitOfWork
	Tokens TokenIssuer
	Now    func() time.Time
	NewID  func() string
}

type APITokenService struct {
	store  UnitOfWork
	tokens TokenIssuer
	now    func() time.Time
	newID  func() string
}

func NewAPITokenService(config APITokenServiceConfig) *APITokenService {
	return &APITokenService{store: config.Store, tokens: config.Tokens, now: config.Now, newID: config.NewID}
}

func (s *APITokenService) Create(
	ctx context.Context,
	principal Principal,
	command CreateAPITokenCommand,
) (APITokenCredential, error) {
	if principal.Kind != PrincipalAdmin || principal.SubjectID == "" {
		return APITokenCredential{}, ErrForbidden
	}
	label, err := domain.NormalizeAPITokenLabel(command.Label)
	if err != nil {
		return APITokenCredential{}, err
	}
	scopes, err := NormalizeScopes(command.Scopes)
	if err != nil {
		return APITokenCredential{}, err
	}
	now := s.now().UTC()
	if command.ExpiresAt != nil && !command.ExpiresAt.After(now) {
		return APITokenCredential{}, ErrInvalidExpiry
	}
	issued, err := s.tokens.New()
	if err != nil {
		return APITokenCredential{}, fmt.Errorf("issue API token: %w", err)
	}
	computedHash := s.tokens.Hash(issued.Raw)
	if issued.Raw == "" || len(issued.Hash) == 0 || len(computedHash) != len(issued.Hash) ||
		subtle.ConstantTimeCompare(computedHash, issued.Hash) != 1 {
		return APITokenCredential{}, errors.New("token issuer returned inconsistent credential")
	}
	record := APITokenRecord{
		ID: s.newID(), AdminID: principal.SubjectID, Label: label,
		TokenHash: slices.Clone(issued.Hash), Scopes: scopes, CreatedAt: now,
		ExpiresAt: cloneTime(command.ExpiresAt),
	}
	payload, err := json.Marshal(struct {
		TokenID string  `json:"tokenId"`
		Scopes  []Scope `json:"scopes"`
	}{TokenID: record.ID, Scopes: scopes})
	if err != nil {
		return APITokenCredential{}, fmt.Errorf("encode API token audit payload: %w", err)
	}
	err = s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if err := repositories.APITokens.Create(ctx, record); err != nil {
			return fmt.Errorf("create API token: %w", err)
		}
		if err := repositories.Audit.Append(ctx, AuditEventRecord{
			ID: s.newID(), Kind: "api-token.created", SubjectKind: "api-token",
			SubjectID: record.ID, Payload: payload, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("audit API token creation: %w", err)
		}
		return nil
	})
	if err != nil {
		return APITokenCredential{}, err
	}
	return APITokenCredential{Token: issued.Raw, Record: publicAPITokenRecord(record)}, nil
}

func (s *APITokenService) List(
	ctx context.Context,
	principal Principal,
	request PageRequest,
) (Page[APITokenRecord], error) {
	if principal.Kind != PrincipalAdmin || principal.SubjectID == "" {
		return Page[APITokenRecord]{}, ErrForbidden
	}
	var page Page[APITokenRecord]
	err := s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		var err error
		page, err = repositories.APITokens.List(ctx, request)
		return err
	})
	if err != nil {
		return Page[APITokenRecord]{}, fmt.Errorf("list API tokens: %w", err)
	}
	page.Items = slices.Clone(page.Items)
	for i := range page.Items {
		page.Items[i] = publicAPITokenRecord(page.Items[i])
	}
	return page, nil
}

func (s *APITokenService) Revoke(ctx context.Context, principal Principal, id string) error {
	if principal.Kind != PrincipalAdmin || principal.SubjectID == "" {
		return ErrForbidden
	}
	now := s.now().UTC()
	payload, err := json.Marshal(struct {
		TokenID string `json:"tokenId"`
	}{TokenID: id})
	if err != nil {
		return fmt.Errorf("encode API token revocation audit payload: %w", err)
	}
	return s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		revoked, err := repositories.APITokens.Revoke(ctx, id, now)
		if err != nil {
			return fmt.Errorf("revoke API token: %w", err)
		}
		if !revoked {
			return ErrNotFound
		}
		if err := repositories.Audit.Append(ctx, AuditEventRecord{
			ID: s.newID(), Kind: "api-token.revoked", SubjectKind: "api-token",
			SubjectID: id, Payload: payload, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("audit API token revocation: %w", err)
		}
		return nil
	})
}

func publicAPITokenRecord(record APITokenRecord) APITokenRecord {
	record.TokenHash = nil
	record.Scopes = slices.Clone(record.Scopes)
	record.ExpiresAt = cloneTime(record.ExpiresAt)
	record.LastUsedAt = cloneTime(record.LastUsedAt)
	record.RevokedAt = cloneTime(record.RevokedAt)
	return record
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

var operationScopes = map[string]Scope{
	"revokeCurrentSession": ScopeTokensWrite,
	"createAPIToken":       ScopeTokensWrite, "listAPITokens": ScopeTokensRead,
	"getAPIToken": ScopeTokensRead, "updateAPIToken": ScopeTokensWrite, "revokeAPIToken": ScopeTokensWrite,
	"createLocation": ScopeLocationsWrite, "listLocations": ScopeLocationsRead,
	"getLocation": ScopeLocationsRead, "updateLocation": ScopeLocationsWrite, "disableLocation": ScopeLocationsWrite,
	"createMonitor": ScopeMonitorsWrite, "listMonitors": ScopeMonitorsRead,
	"getMonitor": ScopeMonitorsRead, "updateMonitor": ScopeMonitorsWrite, "disableMonitor": ScopeMonitorsWrite,
	"createAgentEnrollmentToken": ScopeAgentsWrite, "listAgents": ScopeAgentsRead, "getAgent": ScopeAgentsRead,
	"updateAgent": ScopeAgentsWrite, "revokeAgent": ScopeAgentsWrite, "rotateAgentCredential": ScopeAgentsWrite,
	"getMonitorHealth":         ScopeMonitorsRead,
	"getActiveMonitorIncident": ScopeIncidentsRead, "listIncidents": ScopeIncidentsRead,
	"getIncident": ScopeIncidentsRead, "listIncidentEvents": ScopeIncidentsRead,
	"listDiscoveryCandidates": ScopeDiscoveryRead, "getDiscoveryCandidate": ScopeDiscoveryRead,
	"promoteDiscoveryCandidate": ScopeDiscoveryWrite,
	"createNotificationChannel": ScopeNotificationsWrite, "listNotificationChannels": ScopeNotificationsRead,
	"getNotificationChannel": ScopeNotificationsRead, "updateNotificationChannel": ScopeNotificationsWrite,
	"disableNotificationChannel": ScopeNotificationsWrite, "createNotificationRoute": ScopeNotificationsWrite,
	"listNotificationRoutes": ScopeNotificationsRead, "getNotificationRoute": ScopeNotificationsRead,
	"updateNotificationRoute": ScopeNotificationsWrite, "disableNotificationRoute": ScopeNotificationsWrite,
	"listNotificationDeliveries": ScopeNotificationsRead, "getNotificationDelivery": ScopeNotificationsRead,
	"replayNotificationDelivery": ScopeNotificationsWrite,
	"createMaintenance":          ScopeMaintenanceWrite, "listMaintenance": ScopeMaintenanceRead,
	"getMaintenance": ScopeMaintenanceRead, "deleteMaintenance": ScopeMaintenanceWrite, "endMaintenance": ScopeMaintenanceWrite,
}

func Authorize(operationID string, principal Principal) error {
	required, known := operationScopes[operationID]
	if !known {
		return ErrForbidden
	}
	if principal.Kind == PrincipalAdmin {
		return nil
	}
	if principal.Kind != PrincipalAPIToken {
		return ErrForbidden
	}
	for _, scope := range principal.Scopes {
		if scope == required {
			return nil
		}
	}
	return ErrForbidden
}
