package httpapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestNotificationChannelHandlersAreTypedAndRedacted(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/notifications.db?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	envelope, err := xiscrypto.NewEnvelope(
		1, map[uint32][]byte{1: bytes.Repeat([]byte{9}, 32)},
		bytes.NewReader(bytes.Repeat([]byte{4}, 256)),
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 25, 15, 0, 0, 0, time.UTC)
	server := httpapi.NewServer(httpapi.ServerConfig{
		Notifications: application.NewNotificationAdminService(application.NotificationAdminServiceConfig{
			Store: sqlitestore.NewStore(db), Sealer: envelope,
			Now:   func() time.Time { return now },
			NewID: func() string { return "00000000-0000-4000-8000-000000000001" },
		}),
	})

	const secret = "discord://token@channel"
	var configuration httpapi.NotificationChannelConfigurationInput
	if err := configuration.FromShoutrrrChannelConfigurationInput(httpapi.ShoutrrrChannelConfigurationInput{
		Kind: httpapi.ShoutrrrChannelConfigurationInputKindShoutrrr, ServiceUrl: stringPointer(secret),
	}); err != nil {
		t.Fatal(err)
	}
	created, err := server.CreateNotificationChannel(ctx, httpapi.CreateNotificationChannelRequestObject{
		Body: &httpapi.CreateNotificationChannelRequest{Name: "on call", Enabled: true, Configuration: configuration},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, ok := created.(httpapi.CreateNotificationChannel201JSONResponse)
	if !ok || response.Id != uuid.MustParse("00000000-0000-4000-8000-000000000001") {
		t.Fatalf("CreateNotificationChannel() = %#v", created)
	}
	encoded, err := json.Marshal(response)
	if err != nil || strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "configuration") {
		t.Fatalf("channel response leaked secret/configuration: %s, %v", encoded, err)
	}

	listed, err := server.ListNotificationChannels(ctx, httpapi.ListNotificationChannelsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	page, ok := listed.(httpapi.ListNotificationChannels200JSONResponse)
	if !ok || len(page.Items) != 1 || page.Limit != 50 || page.Offset != 0 {
		t.Fatalf("ListNotificationChannels() = %#v", listed)
	}

	missing, err := server.GetNotificationChannel(ctx, httpapi.GetNotificationChannelRequestObject{
		ChannelId: uuid.MustParse("00000000-0000-4000-8000-000000000099"),
	})
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := missing.(httpapi.GetNotificationChanneldefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 404 || problem.Body.Code != "not_found" {
		t.Fatalf("GetNotificationChannel(missing) = %#v", missing)
	}

	invalid, err := server.CreateNotificationChannel(ctx, httpapi.CreateNotificationChannelRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	validation, ok := invalid.(httpapi.CreateNotificationChanneldefaultApplicationProblemPlusJSONResponse)
	if !ok || validation.StatusCode != 400 {
		t.Fatalf("CreateNotificationChannel(nil body) = %T %#v", invalid, invalid)
	}
}

func stringPointer(value string) *string { return &value }
