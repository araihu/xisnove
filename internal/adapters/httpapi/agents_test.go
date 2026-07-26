package httpapi_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestAgentEnrollmentAndHeartbeatHandlers(t *testing.T) {
	server := newAgentServer(t)
	adminContext := httpapi.ContextWithPrincipal(
		context.Background(),
		application.Principal{Kind: application.PrincipalAdmin, SubjectID: "admin-1"},
	)
	locationID := uuid.MustParse("11111111-1111-4111-8111-111111111111")

	enrollmentResponse, err := server.CreateAgentEnrollmentToken(
		adminContext,
		httpapi.CreateAgentEnrollmentTokenRequestObject{
			Body: &httpapi.CreateAgentEnrollmentTokenJSONRequestBody{
				LocationId:       locationID,
				ExpiresInSeconds: 900,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, ok := enrollmentResponse.(httpapi.CreateAgentEnrollmentToken201JSONResponse)
	if !ok {
		t.Fatalf("response = %#v", enrollmentResponse)
	}

	rawToken := enrollment.Token
	enrollResponse, err := server.EnrollAgent(
		context.Background(),
		httpapi.EnrollAgentRequestObject{
			Body: &httpapi.EnrollAgentJSONRequestBody{
				Token:        &rawToken,
				Name:         "vps-1",
				Capabilities: []httpapi.AgentCapability{httpapi.AgentCapabilityHttp},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, ok := enrollResponse.(httpapi.EnrollAgent201JSONResponse)
	if !ok {
		t.Fatalf("response = %#v", enrollResponse)
	}

	agentContext := httpapi.ContextWithPrincipal(
		context.Background(),
		application.Principal{
			Kind:                 application.PrincipalAgent,
			SubjectID:            enrolled.AgentId.String(),
			CredentialGeneration: 1,
		},
	)
	heartbeatResponse, err := server.HeartbeatAgent(
		agentContext,
		httpapi.HeartbeatAgentRequestObject{
			Body: &httpapi.HeartbeatAgentJSONRequestBody{
				Version:              "v0.1.0",
				CredentialGeneration: 1,
				Capabilities:         []httpapi.AgentCapability{httpapi.AgentCapabilityHttp},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := heartbeatResponse.(httpapi.HeartbeatAgent204Response); !ok {
		t.Fatalf("response = %#v", heartbeatResponse)
	}
}

func TestCreateAgentEnrollmentTokenRequiresAdminPrincipal(t *testing.T) {
	server := newAgentServer(t)
	response, err := server.CreateAgentEnrollmentToken(
		context.Background(),
		httpapi.CreateAgentEnrollmentTokenRequestObject{
			Body: &httpapi.CreateAgentEnrollmentTokenJSONRequestBody{
				LocationId:       uuid.MustParse("11111111-1111-4111-8111-111111111111"),
				ExpiresInSeconds: 900,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := response.(httpapi.CreateAgentEnrollmentTokendefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 401 || problem.Body.Code != "unauthorized" {
		t.Fatalf("response = %#v", response)
	}
}

func newAgentServer(t *testing.T) *httpapi.Server {
	t.Helper()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	location, err := domain.NewLocation(
		"11111111-1111-4111-8111-111111111111",
		"public",
		time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Repositories().Locations.Create(context.Background(), location); err != nil {
		t.Fatal(err)
	}
	ids := []string{
		"22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333",
	}
	nextID := 0
	service := application.NewAgentService(application.AgentServiceConfig{
		Store: store,
		Tokens: xiscrypto.NewTokenIssuer(
			bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)),
		),
		Now: func() time.Time {
			return time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)
		},
		NewID: func() string {
			id := ids[nextID]
			nextID++
			return id
		},
	})
	return httpapi.NewServer(httpapi.ServerConfig{Agents: service})
}
