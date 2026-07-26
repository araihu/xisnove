.PHONY: generate generated-check test module-check storage-check operations-check race-check agent-check ui-check ui-browser-smoke check

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

ui-check:
	GOWORK=off $(MAKE) -C ui check

ui-browser-smoke:
	GOWORK=off $(MAKE) -C ui browser-smoke

check: generated-check module-check storage-check operations-check race-check agent-check ui-check
