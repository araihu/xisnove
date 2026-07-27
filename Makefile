.PHONY: generate generated-check test module-check distribution-contract-check distribution-image-native-check distribution-image-oci-check distribution-helm-check distribution-deploy-check distribution-check runtime-contract-check storage-check operations-check race-check agent-check cli-check cli-workspace-check ui-check ui-browser-smoke kind-edge-e2e check

DISTRIBUTION_ARCH ?= $(shell go env GOARCH)
COMPOSE ?= $(shell if docker compose version >/dev/null 2>&1; then printf 'docker compose'; elif command -v docker-compose >/dev/null 2>&1; then printf 'docker-compose'; else printf 'docker compose'; fi)

generate:
	go generate ./...
	go tool sqlc generate
	cd agent && GOWORK=off go generate ./...

test:
	go test ./...
	cd agent && GOWORK=off go test ./...

generated-check: generate
	git diff --exit-code
	go tool vacuum lint -d api/openapi.yaml
	go tool sqlc diff

module-check:
	cd integration/testdata/external-module && GOWORK=off go test ./...

distribution-contract-check:
	go test ./integration/distribution/contract -count=1

distribution-image-native-check:
	docker buildx bake --print default test-amd64 test-arm64 oci-layout >/dev/null
	docker buildx bake test-$(DISTRIBUTION_ARCH) --load
	go test -race ./integration/distribution/images -skip '^TestOCILayout' -count=1

distribution-image-oci-check:
	docker buildx bake oci-layout
	XISNOVE_REQUIRE_OCI_LAYOUT=1 go test -race ./integration/distribution/images -run '^TestOCILayout' -count=1

distribution-helm-check:
	helm lint charts/xisnove
	helm template xisnove charts/xisnove --values integration/distribution/helm/sqlite-values.yaml >/dev/null
	helm template xisnove charts/xisnove --values integration/distribution/helm/postgres-values.yaml >/dev/null
	helm template xisnove charts/xisnove --values integration/distribution/helm/turso-managed-values.yaml >/dev/null
	go test -race ./integration/distribution/helm -count=1

distribution-deploy-check:
	$(COMPOSE) -f deploy/compose/compose.yaml config >/dev/null
	$(COMPOSE) -f deploy/compose/compose.yaml --profile postgres config >/dev/null
	$(COMPOSE) -f deploy/compose/compose.yaml --profile managed-turso config >/dev/null
	XISNOVE_AGENT_CREDENTIAL_FILE=deploy/compose/secrets/agent-credential.json $(COMPOSE) -f deploy/compose/remote-agent.yaml config >/dev/null
	systemd-analyze verify deploy/systemd/*.service
	shellcheck deploy/raw/*.sh deploy/compose/bootstrap.sh
	go test -race ./integration/distribution/deploy -count=1

distribution-check: distribution-contract-check distribution-image-native-check distribution-image-oci-check distribution-helm-check distribution-deploy-check

runtime-contract-check:
	bash scripts/runtime-contract-check.sh
	go test -race ./cmd/xisnove-server ./internal/buildinfo ./internal/adapters/database ./internal/adapters/migration
	cd agent && GOWORK=off go test -race ./cmd/xisnove-agent ./internal/buildinfo ./internal/observability
	cd cli && GOWORK=off go test -race ./cmd/xisnove ./internal/buildinfo
	cd operator && GOWORK=off go test -race ./cmd/xisnove-operator ./internal/buildinfo
	cd ui && GOWORK=off go test -race ./cmd/server ./internal/buildinfo

storage-check:
	go test -race ./integration -run '^(TestStorageMatrix|TestRetentionUptimeStorageMatrix|TestWorkerRecoveryStorageMatrix)/(SQLite|TursoLocal)$$' -count=1
	XISNOVE_REQUIRE_POSTGRES=1 go test -race ./integration -run '^(TestStorageMatrix|TestRetentionUptimeStorageMatrix|TestWorkerRecoveryStorageMatrix)/Postgres$$' -count=1

operations-check:
	go test -race ./application -run '^(TestDeliveryWorkerBoundsParallelCallsRecoversExpiredClaimAndStops|TestMaintenanceWorkerEmitsExactlyOneSyntheticTransitionAcrossReplicas|TestRetentionWorkerResumesDailyAggregationAndRecomputesLateResults)$$' -count=1
	go test -race ./internal/adapters/shoutrrr ./internal/adapters/alertmanager -run '^TestTransport' -count=1
	go test -race ./internal/adapters/observability -run '^(TestJSONLogger|TestMetrics|TestTracing)' -count=1
	go test -race ./cmd/xisnove-server -run '^Test(Lifecycle|Observability)' -count=1
	go test -race ./integration -run '^TestNotificationJourneyTransportsRetriesResolutionAndRedaction$$' -count=1

race-check:
	go vet ./...
	go test -race ./...

agent-check:
	cd agent && GOWORK=off go vet ./...
	cd agent && GOWORK=off go test -race ./...

cli-check:
	cd cli && GOWORK=off go mod tidy -diff
	cd cli && GOWORK=off go vet ./...
	cd cli && GOWORK=off go test -race ./...

cli-workspace-check:
	go test -race ./cli/...

ui-check:
	GOWORK=off $(MAKE) -C ui check

ui-browser-smoke:
	GOWORK=off $(MAKE) -C ui browser-smoke

kind-edge-e2e:
	bash scripts/kind-edge-e2e.sh

check: generated-check module-check distribution-contract-check runtime-contract-check storage-check operations-check race-check agent-check cli-check cli-workspace-check ui-check
