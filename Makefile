.DEFAULT_GOAL := help
STATICCHECK := go run honnef.co/go/tools/cmd/staticcheck@latest

.PHONY: help build test lint check clean snapshot

help: ## list the targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  make %-10s %s\n", $$1, $$2}'

build: ## build ./bin/laight
	CGO_ENABLED=0 go build -o bin/laight ./cmd/laight

test: ## run the tests
	go test -race ./...

lint: ## gofmt, go vet, staticcheck
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	go vet ./...
	$(STATICCHECK) ./...

check: lint test ## everything CI runs

snapshot: ## a local GoReleaser build into ./dist, nothing published
	go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean

clean: ## remove build output
	rm -rf bin dist
