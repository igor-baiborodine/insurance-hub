################################################################################
# Insurance Hub - Root Makefile
# Follows conventions from CONTRIBUTING.md (Section Makefile Style Guide)
################################################################################

################################################################################
# Includes - domain-specific Makefiles
################################################################################
-include k8s/local-dev/Makefile
-include k8s/qa/Makefile

################################################################################
# Variables
################################################################################
APP_NAME        := insurance-hub
GO_VERSION      := 1.24
SRC_DIR         := ./internal
BIN_DIR         := ./bin
MAKEFLAGS       += --warn-undefined-variables

################################################################################
# Default Target
################################################################################
.PHONY: all
all: help

################################################################################
# Help Target
################################################################################
.PHONY: help
help:
	@echo "Available Make targets:"
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-25s\033[0m %s\n", $$1, $$2}'

################################################################################
# Go Targets
################################################################################
.PHONY: build
build: ## Compile the Go application
	@echo ">> Building $(APP_NAME) (Go $(GO_VERSION))"
	@go build -o $(BIN_DIR)/$(APP_NAME) ./...

################################################################################
# Placeholder Targets (extend as project grows)
################################################################################

.PHONY: lint
lint: ## Run linters (placeholder)
	@echo ">> Running linters... (not yet implemented)"

.PHONY: docker-build
docker-build: ## Build Docker image (placeholder)
	@echo ">> Building Docker image... (not yet implemented)"

.PHONY: k8s-deploy
k8s-deploy: ## Deploy to local Kubernetes cluster (placeholder)
	@echo ">> Deploying to local cluster... (not yet implemented)"
