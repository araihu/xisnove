.PHONY: build check

build:
	GOWORK=off go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate
	GOWORK=off go run ./cmd/x9-site build

check: build
	GOWORK=off go test ./... -count=1
