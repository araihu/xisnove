.PHONY: generate test check browser-smoke

generate:
	GOWORK=off go tool templ generate

test:
	GOWORK=off go test ./... -count=1

check: generate
	git diff --exit-code -- .
	GOWORK=off go test ./... -count=1
	GOWORK=off go vet ./...

browser-smoke:
	GOWORK=off go test -tags=browser ./tests/browser -count=1 -timeout=2m
