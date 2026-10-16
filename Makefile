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

.PHONY: dev
dev: ## Build, start the API with logging, and open the admin panel
	@scripts/dev.sh start

.PHONY: dev-fresh
dev-fresh: ## Start with a new local database
	@scripts/dev.sh start --fresh

.PHONY: dev-stop
dev-stop: ## Stop the development API
	@scripts/dev.sh stop

.PHONY: dev-status
dev-status: ## Report whether the development API is running
	@scripts/dev.sh status

.PHONY: dev-logs
dev-logs: ## Follow the development API log
	@scripts/dev.sh logs

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

# Live provider tests are opt-in. The unit suite must never reach the network
# implicitly, so this target is the only way to run them.
.PHONY: test-live
test-live: ## Run tests that talk to a real fallback provider (needs .env.development)
	@command -v $(GO) >/dev/null || { echo "go is not on PATH"; exit 1; }
	@test -f .env.development || { echo ".env.development is missing; copy .env.example and fill it in"; exit 1; }
	STORVIA_LIVE_FALLBACK_TEST=1 $(GO) test ./internal/app/ -run LiveFallback -v

test-live-drive: ## Run tests that read AND WRITE a real Google Drive account (needs a signed-in console)
	@command -v $(GO) >/dev/null || { echo "go is not on PATH"; exit 1; }
	@test -f .env.development || { echo ".env.development is missing; copy .env.example and fill it in"; exit 1; }
	@echo "These create, read, and delete files in the real Drive account."
	STORVIA_LIVE_DRIVE_TEST=1 STORVIA_LIVE_FALLBACK_TEST=1 $(GO) test ./internal/app/ -run Live -v

.PHONY: console
console: ## Build the Next.js console into the Go binary's asset directory
	@command -v pnpm >/dev/null || { echo "pnpm is not on PATH"; exit 1; }
	cd console && pnpm install --silent && STORVIA_CONSOLE_EXPORT=1 pnpm run build
	rm -rf internal/httpapi/consoleout/assets
	mkdir -p internal/httpapi/consoleout/assets
	cp -R console/.next-export/. internal/httpapi/consoleout/assets/
	@echo "console built into internal/httpapi/consoleout/assets"

.PHONY: console-dev
console-dev: ## Run the Next.js dev server against the Go API on :3001 (Turbopack)
	cd console && pnpm run dev

.PHONY: build-console
build-console: console ## Build the API with the console embedded
	$(GO) build -tags consoleassets ./cmd/storvia-api

.PHONY: test-live-drive

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
	cd sdk/ts && pnpm exec tsc --noEmit --project tsconfig.json

.PHONY: sdk-ts-build
sdk-ts-build: ## Build the TypeScript SDK
	cd sdk/ts && pnpm exec tsc --project tsconfig.json

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