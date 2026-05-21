.PHONY: help deps proto build build-release run test test-coverage lint clean migrate-up migrate-down dev

# Variables
BINARY_NAME=syntheticbrew-srv
PROTO_DIR=api/proto
PROTO_OUT_DIR=api/proto/gen
CONFIG_DIR=config

help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

deps: ## Install dependencies
	go mod download
	go mod tidy

proto: ## Generate protobuf code
	@echo "Generating protobuf code..."
	@mkdir -p $(PROTO_OUT_DIR)
	protoc --proto_path=$(PROTO_DIR) \
		--go_out=$(PROTO_OUT_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_OUT_DIR) --go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/*.proto

build: ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	go build -ldflags "-X main.version=$$(cat ../VERSION) -X main.commit=$$(git rev-parse --short HEAD) -X main.date=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/$(BINARY_NAME) ./cmd/ce

build-release: ## Build obfuscated release binary (requires: go install mvdan.cc/garble@latest)
	@echo "Building obfuscated $(BINARY_NAME)..."
	garble -literals -tiny build -ldflags "-s -w -X main.version=$$(cat ../VERSION) -X main.commit=$$(git rev-parse --short HEAD) -X main.date=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/$(BINARY_NAME) ./cmd/ce

run: build ## Run the server
	@echo "Running $(BINARY_NAME)..."
	./bin/$(BINARY_NAME)

dev: ## Run with hot reload (requires air)
	air

test: ## Run tests
	go test -v -race ./...

test-coverage: ## Run tests with coverage
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint: ## Run linter
	golangci-lint run

clean: ## Clean build artifacts
	rm -rf bin/
	rm -rf $(PROTO_OUT_DIR)
	rm -f coverage.out coverage.html

migrate-up: ## Run database migrations up
	@echo "Running migrations up..."
	liquibase --changeLogFile=internal/infrastructure/database/liquibase/changelog.xml update

migrate-down: ## Rollback last migration
	@echo "Rolling back last migration..."
	liquibase --changeLogFile=internal/infrastructure/database/liquibase/changelog.xml rollback-count 1

.DEFAULT_GOAL := help
