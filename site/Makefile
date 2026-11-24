.PHONY: build check deploy deploy-dry-run

build:
	GOWORK=off go tool templ generate
	GOWORK=off go run ./cmd/x9-site build

check: build
	GOWORK=off go test ./... -count=1

deploy-dry-run: check
	wrangler deploy --dry-run

deploy: check
	wrangler deploy
