SHELL := /bin/bash
-include .env
export

COMPOSE := docker compose -f deploy/docker-compose.yml
CONFIG  := configs/config.yaml

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: build
build: ## Build all binaries
	go build ./...

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: test
test: ## Run network-free tests (set TEST_DATABASE_URL to include DB tests)
	go test ./...

TEST_DSN ?= postgres://bepilot:bepilot@localhost:$${BEPILOT_PG_PORT:-5433}/bepilot_test?sslmode=disable

.PHONY: test-db
test-db: ## Run all tests, including integration tests, on a separate bepilot_test database
	-$(COMPOSE) exec -T postgres psql -U bepilot -d bepilot -c 'CREATE DATABASE bepilot_test' >/dev/null 2>&1
	TEST_DATABASE_URL="$(TEST_DSN)" go test ./... -count=1

.PHONY: bench-render
bench-render: ## Benchmark PDF page rendering (N7a); BEPILOT_RENDER_MODE=multi_threaded for native PDFium
	go test ./internal/parser/pdf/ -run xxx -bench BenchmarkRenderA4 -benchtime 50x

.PHONY: run-api
run-api: ## Run only the HTTP API (needs REDIS_ADDR)
	go run ./cmd/server -config $(CONFIG) -role api

.PHONY: run-worker
run-worker: ## Run only the task workers (needs REDIS_ADDR)
	go run ./cmd/server -config $(CONFIG) -role worker

.PHONY: pdfium-worker
pdfium-worker: ## Build the cgo PDFium worker (needs libpdfium + pkg-config pdfium)
	CGO_ENABLED=1 go build -tags pdfium_cgo -o bin/pdfium-worker ./cmd/pdfium-worker

.PHONY: up
up: ## Start Postgres (pgvector), Redis and MinIO
	$(COMPOSE) up -d
	@echo "waiting for postgres..." && until $(COMPOSE) exec -T postgres pg_isready -U bepilot -d bepilot >/dev/null 2>&1; do sleep 1; done
	@echo "postgres ready on localhost:5432"

.PHONY: down
down: ## Stop the dev services
	$(COMPOSE) down

.PHONY: reset-db
reset-db: ## Drop and recreate the database volume
	$(COMPOSE) down -v && $(MAKE) up

.PHONY: migrate
migrate: ## Apply database migrations
	go run ./cmd/server -config $(CONFIG) -migrate-only

.PHONY: seed
seed: ## Create a dev user and print a fresh API key
	go run ./cmd/seed -config $(CONFIG)

.PHONY: skills-sync
skills-sync: ## Sync skills/ into Postgres + embeddings
	go run ./cmd/skills-sync -config $(CONFIG)

SWAG := go run github.com/swaggo/swag/cmd/swag@v1.16.6

.PHONY: swag
swag: ## Regenerate the OpenAPI spec from handler annotations (docs/)
	$(SWAG) init -g cmd/server/main.go -o docs --parseInternal --parseDepth 2
	@echo "regenerated docs/{docs.go,swagger.json,swagger.yaml}"

.PHONY: docs
docs: swag ## Alias for `swag`; then run `make test` to check route/spec drift
	@echo "run 'make test' — TestRoutesMatchOpenAPISpec catches un-annotated routes"

.PHONY: run
run: ## Run the API server
	go run ./cmd/server -config $(CONFIG)

.PHONY: dev
dev: up migrate ## Bring up DB, migrate, then run the server
	$(MAKE) run

.PHONY: run-web
run-web: ## Run the agent (:9090) and the frontend dev server together
	./scripts/run-web.sh
