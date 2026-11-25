.PHONY: test e2e cover lint fix vet build snapshot release-check clean help

BIN       := anypoint
PKG       := ./cmd/anypoint
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
# Keep in sync with GORELEASER_VERSION in .github/workflows/release.yml.
GORELEASER := go run github.com/goreleaser/goreleaser/v2@v2.20.3

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

test: ## Unit tests with the race detector
	go test -race ./...

e2e: ## End-to-end tests against a Mule CE runtime (MULE_HOME=/path/to/mule-standalone-4.x)
	go test -tags e2e -count=1 -timeout 20m ./e2e

cover: ## Library coverage
	go test -coverprofile=coverage.out ./anypoint && go tool cover -func=coverage.out | tail -1

vet: ## go vet
	go vet ./...

lint: ## golangci-lint (pinned); includes the go fix analyzers (modernize)
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run --build-tags e2e ./e2e/...

fix: ## Apply go fix (what the modernize linter enforces)
	go fix ./...

build: ## Build the CLI for this machine into dist/
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN)$(shell go env GOEXE) $(PKG)

snapshot: ## Build and package every release platform into dist/ (GoReleaser, nothing published)
	$(GORELEASER) release --snapshot --clean --skip=publish,sbom

release-check: ## Validate .goreleaser.yaml
	$(GORELEASER) check

clean: ## Remove build output
	rm -rf dist build coverage.out lib.out test-report.json
