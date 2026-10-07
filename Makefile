# Threavia development tasks.
#
# Tool versions are pinned so that generated code is reproducible.
BUF_VERSION             ?= v1.47.2
PROTOC_GEN_GO_VERSION   ?= v1.36.6
PROTOC_GEN_GRPC_VERSION ?= v1.5.1

GOBIN   ?= 100 1 57 67 100 131shell go env GOPATH)/bin
TEST_POSTGRES_URL ?= postgres://threavia:threavia@localhost:5432/threavia?sslmode=disable
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
	PATH="$(GOBIN):$$PATH" buf breaking --against '.git#branch=main'

.PHONY: build
build: ## Build the Core and Claude backend binaries into bin/
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
	rm -rf bin

.PHONY: test-db
test-db: ## Run the test suite including the database integration tests
	THREAVIA_TEST_POSTGRES_URL=$(TEST_POSTGRES_URL) go test ./...
