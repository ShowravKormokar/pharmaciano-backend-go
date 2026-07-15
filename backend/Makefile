# ==============================================================================
# MediCore ERP Backend
# Makefile
# ==============================================================================

APP_NAME := pharmaciano-erp

GO := go

MAIN_API := ./cmd/api
MAIN_WORKER := ./cmd/worker
MAIN_SEED := ./cmd/seed
MAIN_MIGRATE := ./cmd/migrate

BUILD_DIR := bin

SWAG := swag
MOCKERY := mockery

# ------------------------------------------------------------------------------
# Default
# ------------------------------------------------------------------------------

.DEFAULT_GOAL := help

.PHONY: help

help:
	@echo ""
	@echo "================ MediCore ERP =================="
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
	@echo "Code Generation"
	@echo "  make swagger"
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
# Swagger
# ------------------------------------------------------------------------------

swagger:
	$(SWAG) init \
	-g cmd/api/main.go \
	-o docs/swagger

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
# ------------------------------------------------------------------------------

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-build:
	docker compose build

# ------------------------------------------------------------------------------
# Bootstrap
# ------------------------------------------------------------------------------

bootstrap:
	go mod tidy
	go install github.com/air-verse/air@latest
	go install github.com/swaggo/swag/cmd/swag@latest
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