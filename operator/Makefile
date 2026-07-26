.PHONY: generate verify test envtest

ENVTEST_CACHE ?= $(or $(XDG_CACHE_HOME),$(HOME)/.cache)/xisnove/envtest

generate:
	./hack/update-codegen.sh

verify:
	./hack/verify-codegen.sh

test:
	GOWORK=off go test -race ./...

envtest:
	mkdir -p "$(ENVTEST_CACHE)"
	KUBEBUILDER_ASSETS="$$(GOWORK=off go tool setup-envtest --bin-dir "$(ENVTEST_CACHE)" use 1.36.x -p path)" GOWORK=off go test -count=1 -race ./internal/controller -run '^TestEnvtest'
