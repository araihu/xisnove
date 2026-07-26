.PHONY: generate verify test

generate:
	./hack/update-codegen.sh

verify:
	./hack/verify-codegen.sh

test:
	GOWORK=off go test -race ./...
