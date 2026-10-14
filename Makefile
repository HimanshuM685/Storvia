# Storvia developer tasks.
#
# `make help` lists everything. The Go targets are the authoritative checks;
# `docs/implementation-plan.md` records which of these have actually been run.

.DEFAULT_GOAL := help
SHELL := /bin/bash

# Config and env paths. Override for another environment:
#   make dev CONFIG=config/config.production.json ENV=.env.production
CONFIG ?= config/config.development.json
ENV    ?= .env.development

GO      ?= go
GOFILES := ./cmd ./internal ./sdk/go ./migrations

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- build ----

.PHONY: build
build: ## Compile the API binary
	$(GO) build -o bin/storvia-api ./cmd/storvia-api

.PHONY: run
run: ## Run the API with the development config and env file
	$(GO) run ./cmd/storvia-api -config $(CONFIG) -env $(ENV)

.PHONY: check-config
check-config: ## Validate configuration without serving
	$(GO) run ./cmd/storvia-api -config $(CONFIG) -env $(ENV) -check

# ---------------------------------------------------------------- tests ----

.PHONY: fmt
fmt: ## Format all Go source
	gofmt -w $(GOFILES)

.PHONY: fmt-check
fmt-check: ## Fail if any Go source is unformatted
	@unformatted=$$(gofmt -l $(GOFILES)); \
	if [ -n "$$unformatted" ]; then \
		echo "these files need gofmt:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: test
test: ## Run all Go unit tests
	$(GO) test ./...

.PHONY: test-race
test-race: ## Run all Go tests with the race detector
	$(GO) test -race ./...

.PHONY: test-cover
test-cover: ## Run tests with a coverage summary
	$(GO) test ./... -cover

.PHONY: cover
cover: ## Write a coverage profile to coverage.out
	$(GO) test ./... -coverprofile=coverage.out
	$(GO) tool cover -func=coverage.out | tail -1

# `make check` is the gate the pilot checklist refers to.
.PHONY: check
check: fmt-check vet test ## Run every non-race Go check
	@echo "go checks passed: fmt, vet, test"

.PHONY: check-all
check-all: fmt-check vet test test-race ## Run every Go check including the race detector

# ----------------------------------------------------------------- sdk ----

.PHONY: sdk-ts-check
sdk-ts-check: ## Type-check the TypeScript SDK
	cd sdk/ts && npx tsc --noEmit --project tsconfig.json

.PHONY: sdk-ts-build
sdk-ts-build: ## Build the TypeScript SDK
	cd sdk/ts && npx tsc --project tsconfig.json

.PHONY: sdk-ts-test
sdk-ts-test: ## Run the TypeScript SDK tests
	cd sdk/ts && node --test --experimental-strip-types test/*.test.ts

.PHONY: sdk-go-test
sdk-go-test: ## Run the Go SDK tests
	$(GO) test ./sdk/go/...

# ---------------------------------------------------------------- misc ----

.PHONY: deps
deps: ## Tidy and verify module dependencies
	$(GO) mod tidy
	$(GO) mod verify

.PHONY: clean
clean: ## Remove build output and local databases
	rm -rf bin coverage.out *.db *.db-shm *.db-wal sdk/ts/dist