# Threavia development tasks.
#
# Tool versions are pinned so that generated code is reproducible.
BUF_VERSION             ?= v1.47.2
PROTOC_GEN_GO_VERSION   ?= v1.36.6
PROTOC_GEN_GRPC_VERSION ?= v1.5.1

GOBIN   ?= $(shell go env GOPATH)/bin
TEST_POSTGRES_URL ?= postgres://threavia:threavia@localhost:5432/threavia?sslmode=disable
DEFAULT_BRANCH ?= master
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: tools
tools: ## Install the pinned code generation tools into GOBIN
	go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GRPC_VERSION)

.PHONY: generate
generate: ## Regenerate the Go protobuf and gRPC bindings into gen/
	PATH="$(GOBIN):$$PATH" buf generate

.PHONY: proto-lint
proto-lint: ## Lint the protobuf definitions
	PATH="$(GOBIN):$$PATH" buf lint

.PHONY: proto-breaking
proto-breaking: ## Check the protobuf definitions for breaking changes against main
	PATH="$(GOBIN):$$PATH" buf breaking --against '.git#branch=$(DEFAULT_BRANCH)'

# npm writes this on install, so it is the honest stamp for "the tree matches
# the lockfile". Every web target depends on it, which is what lets `make
# verify` work in a fresh clone without a separate install step.
WEB_DEPS := web/ui/node_modules/.package-lock.json

$(WEB_DEPS): web/ui/package-lock.json web/ui/package.json
	cd web/ui && npm ci --no-audit --no-fund

.PHONY: web-deps
web-deps: $(WEB_DEPS) ## Install the web client dependencies from the lockfile

.PHONY: web
web: $(WEB_DEPS) ## Build the web client into web/ui/dist, which the Go binary embeds
	cd web/ui && npm run build
	# Vite empties the output directory, which takes the placeholder with it.
	# That file is what lets a fresh clone compile the Go package embedding this
	# directory before anything has been built, so put it back.
	touch web/ui/dist/.gitkeep

.PHONY: web-dev
web-dev: $(WEB_DEPS) ## Run the web client dev server on the host, without Docker
	cd web/ui && npm run dev

.PHONY: web-generate
web-generate: $(WEB_DEPS) ## Regenerate the TypeScript API types from api/openapi.yaml
	cd web/ui && npm run generate:api

.PHONY: web-generate-check
web-generate-check: $(WEB_DEPS) ## Fail if the TypeScript API types are not what the contract generates
	@cd web/ui && npm run generate:api >/dev/null 2>&1
	@if ! git diff --quiet -- web/ui/src/api/schema.d.ts; then \
		echo "web/ui/src/api/schema.d.ts is stale, run: make web-generate"; \
		git diff --stat -- web/ui/src/api/schema.d.ts; \
		exit 1; \
	fi

.PHONY: web-lint
web-lint: $(WEB_DEPS) ## Type-check and lint the web client
	cd web/ui && npm run typecheck && npm run lint

.PHONY: web-demo-check
web-demo-check: ## Fail if the client calls an API route the /demo server does not answer
	cd web/ui && npm run check:demo

# The VS Code extension has its own dependencies, stamped the same way.
VSCODE_DEPS := clients/vscode/node_modules/.package-lock.json

$(VSCODE_DEPS): clients/vscode/package-lock.json clients/vscode/package.json
	cd clients/vscode && npm ci --no-audit --no-fund

.PHONY: vscode-deps
vscode-deps: $(VSCODE_DEPS) ## Install the VS Code extension dependencies from the lockfile

.PHONY: vscode-generate
vscode-generate: $(VSCODE_DEPS) ## Regenerate the VS Code extension's API types from api/openapi.yaml
	cd clients/vscode && npm run generate:api

.PHONY: vscode-check
vscode-check: $(VSCODE_DEPS) ## Check the VS Code extension: API types, type-check, lint and tests
	@cd clients/vscode && npm run generate:api >/dev/null 2>&1
	@if ! git diff --quiet -- clients/vscode/src/api/schema.d.ts; then \
		echo "clients/vscode/src/api/schema.d.ts is stale, run: make vscode-generate"; \
		git diff --stat -- clients/vscode/src/api/schema.d.ts; \
		exit 1; \
	fi
	cd clients/vscode && npm run typecheck && npm run lint && npm test

.PHONY: vscode-package
vscode-package: $(VSCODE_DEPS) ## Package the VS Code extension into clients/vscode/threavia.vsix
	cd clients/vscode && npm run package

.PHONY: dev-web
dev-web: ## Serve the web client with hot reload on http://localhost:5173
	docker compose --profile web up -d --build web
	@echo
	@echo "Web client:  http://localhost:5173"
	@echo "It proxies to Core on the host at :8080, so run 'make run-core' too."
	@echo "Editing web/ui reloads the browser; the Go binary is untouched."

.PHONY: build
build: web ## Build the web client, then the Core and Claude backend binaries into bin/
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/threavia-core ./cmd/threavia-core
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/threavia-backend-claude ./cmd/threavia-backend-claude

.PHONY: build-go
build-go: ## Build only the Go binaries, leaving whatever client is already built
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/threavia-core ./cmd/threavia-core
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/threavia-backend-claude ./cmd/threavia-backend-claude

.PHONY: test
test: ## Run the test suite
	go test ./...

.PHONY: test-race
test-race: ## Run the test suite with the race detector (needs a C compiler)
	CGO_ENABLED=1 go test -race ./...

.PHONY: lint
lint: proto-lint ## Run go vet and the protobuf linter
	go vet ./...

.PHONY: fmt
fmt: ## Format the Go sources
	gofmt -w $(shell find . -name '*.go' -not -path './gen/*')

.PHONY: tidy
tidy: ## Tidy the Go module
	go mod tidy

.PHONY: dev-up
dev-up: ## Start the local development dependencies (PostgreSQL)
	docker compose up -d

.PHONY: dev-up-storage
dev-up-storage: ## Also start and configure the S3-compatible object storage
	docker compose --profile objectstore up -d
	./deploy/dev/garage-init.sh

.PHONY: dev-down
dev-down: ## Stop the local development dependencies
	docker compose down

.PHONY: dev-reset
dev-reset: ## Stop the local development dependencies and delete their data
	docker compose down -v

.PHONY: migrate
migrate: ## Apply the database migrations and exit
	go run ./cmd/threavia-core -migrate-only

.PHONY: run-core
run-core: ## Run Core against the local development dependencies
	go run ./cmd/threavia-core

.PHONY: run-backend
run-backend: ## Run the Claude backend against a local Core
	go run ./cmd/threavia-backend-claude

.PHONY: clean
clean: ## Remove the build output
	rm -rf web/ui/dist/assets web/ui/dist/index.html
	rm -rf bin

.PHONY: test-db
test-db: ## Run the test suite including the database integration tests
	THREAVIA_TEST_POSTGRES_URL=$(TEST_POSTGRES_URL) go test ./...

.PHONY: verify
verify: fmt-check tidy-check generate-check web-generate-check web-demo-check vscode-check lint test ## Run every check CI runs

.PHONY: fmt-check
fmt-check: ## Fail if any hand-written Go source is not gofmt-ed
	@unformatted="$$(gofmt -l $$(find . -name '*.go' -not -path './gen/*'))"; \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt-ed:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: tidy-check
tidy-check: ## Fail if go.mod or go.sum are not tidy
	@cp go.mod go.mod.bak && cp go.sum go.sum.bak
	@go mod tidy
	@status=0; \
	if ! diff -q go.mod go.mod.bak >/dev/null || ! diff -q go.sum go.sum.bak >/dev/null; then \
		echo "go.mod or go.sum is not tidy, run: make tidy"; status=1; \
	fi; \
	mv go.mod.bak go.mod && mv go.sum.bak go.sum; exit $$status

.PHONY: generate-check
generate-check: generate ## Fail if the committed generated code is out of date
	@if ! git diff --quiet -- gen; then \
		echo "gen/ is out of date, run: make generate"; git diff --stat -- gen; exit 1; \
	fi

.PHONY: docker
docker: ## Build both container images locally
	docker build -f deploy/docker/core.Dockerfile --build-arg VERSION=$(VERSION) -t threavia-core:$(VERSION) .
	docker build -f deploy/docker/backend-claude.Dockerfile --build-arg VERSION=$(VERSION) -t threavia-backend-claude:$(VERSION) .
