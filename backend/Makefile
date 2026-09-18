# ==============================================================================
# Pharmaciano ERP Backend
# Makefile
# ==============================================================================

APP_NAME := pharmaciano-erp

GO := go

MAIN_API := ./cmd/api
MAIN_WORKER := ./cmd/worker
MAIN_SEED := ./cmd/seed
MAIN_MIGRATE := ./cmd/migrate

BUILD_DIR := bin

MOCKERY := mockery

# ------------------------------------------------------------------------------
# Default
# ------------------------------------------------------------------------------

.DEFAULT_GOAL := help

.PHONY: help

help:
	@echo ""
	@echo "================ Pharmaciano ERP =================="
	@echo ""
	@echo "Development"
	@echo "  make run                 Start API server"
	@echo "  make worker              Start background worker"
	@echo "  make seed                Run seed"
	@echo "  make migrate             Run migration service"
	@echo ""
	@echo "Database"
	@echo "  make migrate-up"
	@echo "  make migrate-down"
	@echo "  make migrate-force VERSION=1"
	@echo ""
	@echo "Quality"
	@echo "  make fmt"
	@echo "  make lint"
	@echo "  make vet"
	@echo "  make test"
	@echo "  make coverage"
	@echo ""
	@echo "API Contract"
	@echo "  make oapi-check         Validate split YAML refs (fast)"
	@echo "  make oapi-bundle-docs   Regenerate served /api-docs spec (self-contained)"
	@echo "  make oapi-lint           Redocly lint bundled spec"
	@echo "  make oapi-bundle         Bundle split YAML → single spec"
	@echo "  make oapi-validate       kin-openapi runtime validate (builds with -tags oapival)"
	@echo "  make oapi-generate       oapi-codegen → Go models + strict-server"
	@echo "  make oapi-client         oapi-codegen → Go SDK client"
	@echo "  make oapi-diff           oasdiff breaking-change detection"
	@echo "  make oapi-drift          Check generated code is up to date"
	@echo "  make oapi-all            Run full pipeline (check+lint+bundle+generate+drift)"
	@echo "  make oapi-serve          Scalar reference docs on :6060"
	@echo ""
	@echo "Code Generation"
	@echo "  make mocks"
	@echo ""
	@echo "Build"
	@echo "  make build"
	@echo "  make clean"
	@echo ""
	@echo "Docker"
	@echo "  make docker-up"
	@echo "  make docker-down"
	@echo "  make docker-build"
	@echo ""

# ------------------------------------------------------------------------------
# Development
# ------------------------------------------------------------------------------

run:
	$(GO) run $(MAIN_API)

worker:
	$(GO) run $(MAIN_WORKER)

seed:
	$(GO) run $(MAIN_SEED)

migrate:
	$(GO) run $(MAIN_MIGRATE)

# ------------------------------------------------------------------------------
# Build
# ------------------------------------------------------------------------------

build:
	@mkdir -p $(BUILD_DIR)

	$(GO) build -o $(BUILD_DIR)/api $(MAIN_API)

	$(GO) build -o $(BUILD_DIR)/worker $(MAIN_WORKER)

	$(GO) build -o $(BUILD_DIR)/seed $(MAIN_SEED)

	$(GO) build -o $(BUILD_DIR)/migrate $(MAIN_MIGRATE)

clean:
	rm -rf $(BUILD_DIR)

# ------------------------------------------------------------------------------
# Formatting
# ------------------------------------------------------------------------------

fmt:
	gofmt -w .

imports:
	goimports -w .

tidy:
	go mod tidy

vet:
	go vet ./...

lint:
	golangci-lint run

# ------------------------------------------------------------------------------
# Testing
# ------------------------------------------------------------------------------

test:
	go test ./...

coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out

# ------------------------------------------------------------------------------
# OpenAPI / API contract
#
# The OpenAPI 3.1 split-YAML tree under api/openapi/** is the single source of
# truth (no annotations over handlers). Pipeline: validate refs → lint → bundle
# to build/openapi.bundled.yaml → generate Go code / SDK → detect drift.
#
#   oapi-check    verify.go (dependency-free) — fails in <1s on a broken ref
#   oapi-lint     Redocly CLI — authoritative lint against recommended rules
#   oapi-bundle   Redocly bundles split files → build/openapi.bundled.yaml
#   oapi-generate oapi-codegen → internal/api/gen/v1 (models+server+spec)
#   oapi-client   oapi-codegen → internal/api/client/v1 (Go SDK)
#   oapi-diff     oasdiff — fail on breaking changes vs.<base>
#   oapi-drift    regenerate into a temp dir + diff — CI gate
# ------------------------------------------------------------------------------

# Tool variables. Locations honoured after `make bootstrap` (GOBIN on PATH).
REDOCLY   := redocly
OAICODEGEN := oapi-codegen
OASDIFF   := oasdiff
OAS_SPEC_TREE := api/openapi
OAS_BUNDLED  := build/openapi.bundled.yaml
OAS_TMP  := build
# Baseline for oapi-diff: path to a previously-contracted spec to compare against.
# Defaults to the committed bundled spec; override per-call e.g. make oapi-diff BASE=vendor/openapi.v0.yaml
# Baseline for oapi-diff. Defaults to the already-committed bundled spec; override
# per-call to diff a release baseline into the working tree, e.g.
#   make oapi-diff BASE=vendor/openapi.v0.yaml
# To diff the current spec against the last published contract, pass an explicit
# BASE (self-diffing is meaningless).
OAS_BASE ?=

# File-producing target for the bundled spec. `make oapi-bundle` is a convenience
# wrapper; the other oapi-* targets depend on this real file so regeneration is
# triggered automatically when the split tree changes (contract sources listed as
# prerequisites via a wildcard glob).
OAS_SOURCES := $(wildcard $(OAS_SPEC_TREE)/openapi.yaml $(OAS_SPEC_TREE)/paths/*.yaml $(OAS_SPEC_TREE)/components/**/*.yaml)

$(OAS_BUNDLED): $(OAS_SOURCES)
	@mkdir -p $(OAS_TMP)
	$(REDOCLY) bundle $(OAS_SPEC_TREE)/openapi.yaml \
		--output $(OAS_BUNDLED) \
		--config=$(OAS_SPEC_TREE)/config/redocly.yaml
	@echo "bundled -> $(OAS_BUNDLED)"

.PHONY: oapi-check oapi-lint oapi-bundle oapi-validate oapi-generate oapi-client oapi-diff oapi-drift oapi-all oapi-serve oapi-deps

# Fast internal gate: cross-file $ref resolution + structural checks, no deps.
oapi-check:
	python3 scripts/openapi/validate.py

# Redocly authoritative lint. Bundle first so refs are flattened and the linter
# sees the same single document oapi-codegen consumes.
oapi-lint: $(OAS_BUNDLED)
	$(REDOCLY) lint $(OAS_BUNDLED) --config=$(OAS_SPEC_TREE)/config/redocly.yaml

# Convenience wrapper around the file-producing $(OAS_BUNDLED) target. Also
# regenerates the self-contained viewer spec the API serves at /api-docs.
oapi-bundle: $(OAS_BUNDLED) oapi-bundle-docs

# Self-contained spec (all refs folded) for the Scalar viewer mounted by Gin at
# /api-docs. bundle.py inlines external-file AND file-local refs -> zero $ref.
.PHONY: oapi-bundle-docs
oapi-bundle-docs:
	python3 scripts/openapi/bundle.py \
		--out api/scalar/openapi.yaml \
		--server http://localhost:8080/api/v1,v1 (current) \
		--server http://localhost:8080/api/v2,v2 (future)
	@echo "docs spec -> api/scalar/openapi.yaml (served at /api-docs/openapi.yaml)"

# Regenerate Go models + gin strict-server from the bundled spec.
oapi-generate: $(OAS_BUNDLED)
	$(OAICODEGEN) generate --config=$(OAS_SPEC_TREE)/config/oapi-codegen.yaml

# Regenerate the optional typed Go SDK client.
oapi-client: $(OAS_BUNDLED)
	$(OAICODEGEN) generate --config=$(OAS_SPEC_TREE)/config/client.yaml

# kin-openapi runtime validation build (enforces contract inside a tagged build).
oapi-validate:
	$(GO) build -tags oapival ./internal/api/server/...

# Breaking-change detector (API reference): fail if the working spec would break
# a consumer of $(OAS_BASE). Defaults to comparing the working tree against the
# already-committed bundled spec. Use BASE=... to diff against an external baseline.
oapi-diff: $(OAS_BUNDLED)
	@test -n "$(OAS_BASE)" || { \
		echo "no BASE given; pass the published baseline, e.g. make oapi-diff BASE=build/openapi.v0.yaml"; \
		echo "(self-diffing would be meaningless)"; exit 1; }
	@test -f "$(OAS_BASE)" || { echo "baseline $(OAS_BASE) missing"; exit 1; }
	$(OASDIFF) $(OAS_BASE) $(OAS_BUNDLED) \
		--format text \
		--fail-on ERR \
		--err-ignored 6461 \
		--err-ignore=remove-response \
		--warn-ignored 3651,3652,4054 \
		--output output.yaml

# Drift gate for CI: generate into a scratch dir and diff against committed output.
oapi-drift: $(OAS_BUNDLED)
	@mkdir -p $(OAS_TMP)/drift
	@cp $(OAS_SPEC_TREE)/config/oapi-codegen.yaml $(OAS_TMP)/drift/config.yaml
	@sed -i 's#output: internal/api/gen/v1/api.gen.go#output: build/drift/api.gen.go#' \
		$(OAS_TMP)/drift/config.yaml
	rm -f build/drift/api.gen.go
	$(OAICODEGEN) generate --config=$(OAS_TMP)/drift/config.yaml
	@set -e; if ! diff -q internal/api/gen/v1/api.gen.go build/drift/api.gen.go >/dev/null; then \
		echo "DRIFT: api.gen.go is out of date — run 'make oapi-generate' and commit."; \
		exit 1; fi
	@echo "no drift: generated code matches the contract"

# Default dev view: serve Scalar reference docs against :6060.
oapi-serve: $(OAS_BUNDLED)
	@echo "serving Scalar reference at http://localhost:6060/scalar/index.html"
	python3 -m http.server 6060 --directory api

# Full pipeline — the same order CI runs.
oapi-all: oapi-check oapi-lint oapi-bundle oapi-generate oapi-client oapi-drift

# Install the OpenAPI toolchain (Bootstrap step). Prefer pinned versions.
oapi-deps:
	@echo "Installing OpenAPI toolchain (pinned)"
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
	npm install -g @redocly/cli@latest
	go install github.com/Tufin/oasdiff@latest

# ------------------------------------------------------------------------------
# Mockery
# ------------------------------------------------------------------------------

mocks:
	$(MOCKERY)

# ------------------------------------------------------------------------------
# Migration
# ------------------------------------------------------------------------------

migrate-up:
	migrate \
	-path migrations \
	-database $$DATABASE_URL \
	up

migrate-down:
	migrate \
	-path migrations \
	-database $$DATABASE_URL \
	down 1

migrate-force:
	migrate \
	-path migrations \
	-database $$DATABASE_URL \
	force $(VERSION)

# ------------------------------------------------------------------------------
# Docker
#
# The compose project lives in deployments/docker — its services reference
# relative paths (nginx confs, prometheus configs, grafana dashboards) and a
# local .env, so every docker* target cd's into that directory before invoking
# compose. Without this, `docker compose up` from the repo root would find no
# compose file and the nginx/config mounts would be mis-resolved.
#
#   make docker-up        boot the full stack (api+worker+db+redis+nginx+obs)
#   make docker-down      stop the stack
#   make docker-logs      tail all service logs
#   make docker-ps        show container status
#   make docker-verify    boot-needed-then-run endpoint verification
# ------------------------------------------------------------------------------

DOCKER_BASE := deployments/docker

docker-up:
	cd $(DOCKER_BASE) && docker compose up -d

docker-down:
	cd $(DOCKER_BASE) && docker compose down

docker-logs:
	cd $(DOCKER_BASE) && docker compose logs -f

docker-ps:
	cd $(DOCKER_BASE) && docker compose ps

docker-build:
	cd $(DOCKER_BASE) && docker compose build

# To avoid port conflicts with a locally-running stack, target the compose
# "dev" profile' set. Alias kept simple; see deployments/docker for full control.
docker-verify:
	cd $(DOCKER_BASE) && docker compose up -d --wait
	scripts/verify-api.sh

# ------------------------------------------------------------------------------
# Bootstrap
# ------------------------------------------------------------------------------

bootstrap:
	go mod tidy
	go install github.com/air-verse/air@latest
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
	go install github.com/Tufin/oasdiff@latest
	npm install -g @redocly/cli@latest
	go install github.com/vektra/mockery/v2@latest
	go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# ------------------------------------------------------------------------------
# Git
# ------------------------------------------------------------------------------

status:
	git status

push:
	git push

pull:
	git pull

# ------------------------------------------------------------------------------
# CI Local
# ------------------------------------------------------------------------------

ci:
	gofmt -w .
	go vet ./...
	golangci-lint run
	go test ./...