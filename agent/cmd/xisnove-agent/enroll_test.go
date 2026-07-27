package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/araihu/xisnove/agent/credentials"
	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/google/uuid"
)

func TestEnrollCommandRetriesSameCallerCredentialAndThenReusesBundle(t *testing.T) {
	var mu sync.Mutex
	var requests []controlplane.EnrollAgentRequest
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body controlplane.EnrollAgentRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "bad body", http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, body)
		keys = append(keys, request.Header.Get("Idempotency-Key"))
		attempt := len(requests)
		mu.Unlock()
		if attempt == 1 {
			http.Error(writer, "uncertain result", http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(controlplane.EnrolledAgent{
			AgentId:    uuid.MustParse("11111111-1111-4111-8111-111111111111"),
			Credential: *body.Credential, CredentialGeneration: 1,
		})
	}))
	t.Cleanup(server.Close)
	directory := t.TempDir()
	tokenFile := filepath.Join(directory, "token")
	credentialFile := filepath.Join(directory, "credential.json")
	if err := os.WriteFile(tokenFile, []byte("one-time-enrollment-token-0123456789012345\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--url", server.URL, "--token-file", tokenFile, "--credential-file", credentialFile, "--name", "edge", "--capabilities", "http"}
	if err := enrollCommand(context.Background(), args); err == nil {
		t.Fatal("first uncertain enrollment error = nil")
	}
	if err := enrollCommand(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	bundle, err := (credentials.FileProvider{Path: credentialFile}).Current(context.Background())
	if err != nil || bundle.Generation != 1 {
		t.Fatalf("bundle = %#v, %v", bundle, err)
	}
	if err := os.Remove(tokenFile); err != nil {
		t.Fatal(err)
	}
	if err := enrollCommand(context.Background(), args); err != nil {
		t.Fatalf("reuse without token: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0].Credential == nil || requests[1].Credential == nil || *requests[0].Credential != *requests[1].Credential || *requests[0].Credential != bundle.Credential || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("requests=%#v keys=%#v bundle=%#v", requests, keys, bundle)
	}
}

func TestEnrollCommandRejectsUnsafeTokenFile(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("one-time-enrollment-token-0123456789012345"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := enrollCommand(context.Background(), []string{
		"--url", "https://example.test", "--token-file", tokenFile,
		"--credential-file", filepath.Join(t.TempDir(), "credential.json"),
		"--name", "edge", "--capabilities", "http",
	})
	if err == nil {
		t.Fatal("unsafe token error = nil")
	}
}
