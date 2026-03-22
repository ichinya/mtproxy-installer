SHELL := bash
.ONESHELL:
.SHELLFLAGS := -eu -o pipefail -c
.DELETE_ON_ERROR:
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

.DEFAULT_GOAL := help

PROJECT ?= mtproxy-installer
PROVIDER ?= telemt
DOCKER_SERVICE ?= $(PROVIDER)

ENV_FILE ?= .env
ROOT_COMPOSE_FILE ?= docker-compose.yml
PROVIDER_DIR ?= providers/$(PROVIDER)
PROVIDER_ENV_FILE ?= $(PROVIDER_DIR)/.env
PROVIDER_COMPOSE_FILE ?= $(PROVIDER_DIR)/docker-compose.yml
DATA_DIR ?= $(PROVIDER_DIR)/data

DOCKER ?= docker
COMPOSE ?= $(DOCKER) compose
SHELLCHECK ?= shellcheck
SHFMT ?= shfmt

ROOT_ENV_SOURCE := $(if $(wildcard $(ENV_FILE)),$(ENV_FILE),.env.example)
PROVIDER_ENV_SOURCE := $(if $(wildcard $(PROVIDER_ENV_FILE)),$(PROVIDER_ENV_FILE),$(PROVIDER_DIR)/.env.example)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || printf 'dev')
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf 'unknown')
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

ROOT_COMPOSE_CMD = $(COMPOSE) -f $(ROOT_COMPOSE_FILE) --project-directory . --env-file $(ROOT_ENV_SOURCE)
PROVIDER_COMPOSE_CMD = $(COMPOSE) -f $(PROVIDER_COMPOSE_FILE) --project-directory $(PROVIDER_DIR) --env-file $(PROVIDER_ENV_SOURCE)

-include $(ENV_FILE)
-include $(PROVIDER_ENV_FILE)

.PHONY: help install setup build test lint fmt fmt-check clean ci dev docker-build docker-run docker-stop docker-logs docker-ps docker-clean docker-dev docker-dev-build docker-dev-down docker-prod-build docker-prod-run compose-check health proxy-link uninstall update

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z0-9_.-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2} /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)}' $(MAKEFILE_LIST)

##@ Setup
install: setup ## Prepare local example configuration and data directories

setup: ## Copy example env/config files when they are missing
	mkdir -p "$(DATA_DIR)"
	if [ "$(PROVIDER)" = "telemt" ]; then mkdir -p "$(DATA_DIR)/cache" "$(DATA_DIR)/tlsfront"; fi
	if [ ! -f "$(ENV_FILE)" ]; then cp .env.example "$(ENV_FILE)"; fi
	if [ ! -f "$(PROVIDER_ENV_FILE)" ]; then cp "$(PROVIDER_DIR)/.env.example" "$(PROVIDER_ENV_FILE)"; fi
	printf 'Prepared %s (provider=%s, %s, %s)\n' "$(PROJECT)" "$(PROVIDER)" "$(VERSION)" "$(COMMIT)"

##@ Validation
build: compose-check ## Validate root and provider Docker Compose manifests
	printf 'Compose manifests are valid for %s at %s\n' "$(PROJECT)" "$(BUILD_TIME)"

test: ## Run shell and configuration smoke checks
	bash -n install.sh
	bash -n uninstall.sh
	bash -n update.sh
	test -f .env.example
	test -f "$(PROVIDER_DIR)/.env.example"
	printf 'Smoke checks passed for %s (provider=%s)\n' "$(PROJECT)" "$(PROVIDER)"

lint: ## Run shell linting for installer scripts
	command -v "$(SHELLCHECK)" >/dev/null 2>&1 || { printf 'shellcheck is required for lint\n' >&2; exit 1; }
	"$(SHELLCHECK)" install.sh uninstall.sh update.sh

fmt: ## Format shell sources in place
	command -v "$(SHFMT)" >/dev/null 2>&1 || { printf 'shfmt is required for fmt\n' >&2; exit 1; }
	"$(SHFMT)" -w install.sh uninstall.sh update.sh

fmt-check: ## Check shell formatting without rewriting files
	command -v "$(SHFMT)" >/dev/null 2>&1 || { printf 'shfmt is required for fmt-check\n' >&2; exit 1; }
	"$(SHFMT)" -d install.sh uninstall.sh update.sh

ci: fmt-check lint test build ## Run the local CI verification pipeline

compose-check: ## Render root and provider compose files with current env files
	command -v "$(DOCKER)" >/dev/null 2>&1 || { printf 'docker is required for compose-check\n' >&2; exit 1; }
	$(ROOT_COMPOSE_CMD) config >/dev/null
	$(PROVIDER_COMPOSE_CMD) config >/dev/null

##@ Local Runtime
dev: docker-dev ## Start the local provider stack

health: ## Query the local provider health endpoint (telemt only)
	@if [ "$(PROVIDER)" != "telemt" ]; then \
		printf 'health endpoint only available for telemt provider\n' >&2; \
		exit 1; \
	fi
	curl -fsS "http://127.0.0.1:$${API_PORT:-9091}/v1/health"

proxy-link: ## Print proxy link from the local API (telemt only)
	@if [ "$(PROVIDER)" != "telemt" ]; then \
		printf 'proxy-link only available for telemt provider (mtg has no HTTP API)\n' >&2; \
		exit 1; \
	fi
	curl -fsS "http://127.0.0.1:$${API_PORT:-9091}/v1/users"

clean: ## Remove local provider cache directories
	rm -rf "$(DATA_DIR)/cache" "$(DATA_DIR)/tlsfront"

##@ Docker
docker-build: ## Pull the configured provider image from the registry
	$(ROOT_COMPOSE_CMD) pull "$(DOCKER_SERVICE)"

docker-run: docker-dev ## Start the provider container stack

docker-stop: ## Stop running provider containers without removing them
	$(ROOT_COMPOSE_CMD) stop

docker-logs: ## Tail provider container logs
	$(ROOT_COMPOSE_CMD) logs -f "$(DOCKER_SERVICE)"

docker-ps: ## Show Docker Compose service status
	$(ROOT_COMPOSE_CMD) ps

docker-clean: ## Stop the stack and remove containers, networks, and volumes
	$(ROOT_COMPOSE_CMD) down --remove-orphans --volumes

docker-dev: setup ## Start the local Docker Compose stack
	$(ROOT_COMPOSE_CMD) up -d

docker-dev-build: setup ## Refresh images and recreate the local stack
	$(ROOT_COMPOSE_CMD) up -d --pull always

docker-dev-down: ## Stop the local Docker Compose stack
	$(ROOT_COMPOSE_CMD) down --remove-orphans

docker-prod-build: ## Pull the production image declared in Compose
	$(ROOT_COMPOSE_CMD) pull "$(DOCKER_SERVICE)"

docker-prod-run: setup ## Start the production-style stack from the root compose file
	$(ROOT_COMPOSE_CMD) up -d

##@ Remote Operations
uninstall: ## Show uninstall command
	@printf 'curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/uninstall.sh | sudo bash\n'

update: ## Show update command
	@printf 'curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/update.sh | sudo bash\n'
