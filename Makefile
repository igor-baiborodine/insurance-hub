################################################################################
# Insurance Hub - Root Makefile
# Follows conventions from CONTRIBUTING.md (Section Makefile Style Guide)
################################################################################

################################################################################
# Includes - child domain-specific Makefiles
################################################################################
include k8s/Makefile
include k8s/bootstrap/Makefile

################################################################################
# Variables
################################################################################
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
	@grep -h -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2}'

################################################################################
# Go Targets
################################################################################
.PHONY: go-scaffold-bootstrap-tools go-scaffold-build go-scaffold-check go-scaffold-run
go-scaffold-bootstrap-tools: ## Install and verify pinned tools for the Go service scaffold only
	$(MAKE) -C templates/go-service bootstrap-tools

go-scaffold-build: ## Build the Go service scaffold module only
	$(MAKE) -C templates/go-service build

go-scaffold-check: ## Run all non-mutating checks for the Go service scaffold module only
	$(MAKE) -C templates/go-service check

go-scaffold-run: ## Run the Go service scaffold server locally
	$(MAKE) -C templates/go-service run

################################################################################
# Java Targets
################################################################################

.PHONY: java-all-build
java-all-build: ## Build all Java microservices
	@echo "Building all Java microservices without tests..."
	@bash legacy/build-microservices-without-tests.sh
	@echo "✅ All Java microservices built successfully."

################################################################################
# Frontend Targets
################################################################################

.PHONY: frontend-build
frontend-build: ## Build frontend (Vue app)
	@NODE_VERSION=$$(node -v); \
	if [ "$$NODE_VERSION" != "v12.22.12" ]; then \
		echo "ERROR: Incorrect Node.js version ($$NODE_VERSION). Please use v12.22.12 (hint: nvm use 12.22.12)"; \
		exit 1; \
	fi
	@echo "Building frontend (Vue app)..."
	@bash legacy/build-frontend.sh
	@echo "✅ Frontend (Vue app) built successfully."

################################################################################
# Docker Targets
################################################################################

.PHONY: docker-java-svc-build
docker-java-svc-build: _svc-name-check ## Build a Docker image for a Java service in the 'legacy' folder. Usage: make docker-java-svc-build SVC_NAME=<svc-name>
	@SVC_FOLDER="legacy/$(SVC_NAME)-service"; \
	IMAGE_NAME="insurance-hub-$(SVC_NAME)-api-legacy:latest"; \
	if [ "$(SVC_NAME)" = "agent-portal-gateway" ]; then \
		SVC_FOLDER="legacy/$(SVC_NAME)"; \
		IMAGE_NAME="insurance-hub-$(SVC_NAME)-legacy:latest"; \
	fi; \
	if [ "$(SVC_NAME)" = "document" ]; then \
		SVC_FOLDER="legacy/$(SVC_NAME)s-service"; \
	fi; \
	echo "🔨 Building Docker image for Java service '$(SVC_NAME)' from '$$SVC_FOLDER'..."; \
	docker build --no-cache -f "$$SVC_FOLDER/Dockerfile" "$$SVC_FOLDER" -t "$$IMAGE_NAME"; \
	docker image prune -f; \
	echo "✅ Docker image '$$IMAGE_NAME' built successfully."

.PHONY: docker-java-svc-all-build
docker-java-svc-all-build: ## Build all Java service Docker images. Usage: make docker-java-svc-all-build
	@echo "🔨 Building all Java service Docker images..."
	@SVC_NAMES="agent-portal-gateway,auth,product,policy-search,dashboard,policy,pricing,document,payment,chat" ; \
	SVC_NAMES=$$(echo "$$SVC_NAMES" | tr ',' ' ') ; \
	for svc in $$SVC_NAMES ; do \
		$(MAKE) docker-java-svc-build SVC_NAME=$$svc ; \
	done ; \
	docker image prune -f; \
	echo "✅ All Java service Docker images built!" ; \

.PHONY: docker-frontend-build
docker-frontend-build: ## Build the Docker image for the Vue frontend in the 'legacy' folder. Usage: make docker-frontend-build
	@SVC_FOLDER="legacy/web-vue"; \
	IMAGE_NAME="insurance-hub-web-vue-legacy:latest"; \
	echo "🔨 Building Docker image for Frontend from '$$SVC_FOLDER'..."; \
	docker build --no-cache -f "$$SVC_FOLDER/Dockerfile" "$$SVC_FOLDER" -t "$$IMAGE_NAME"; \
	docker image prune -f; \
	echo "✅ Docker image '$$IMAGE_NAME' built successfully."

.PHONY: legacy-all-build
legacy-all-build: ## Build all legacy artifacts sequentially (Java -> Frontend -> Docker). Usage: legacy-all-build
	@echo "🚀 Building all legacy artifacts (Java -> Frontend -> Docker)..."
	$(MAKE) java-all-build
	$(MAKE) docker-java-svc-all-build
	$(MAKE) frontend-build
	$(MAKE) docker-frontend-build
	@echo "✅ All legacy artifacts built successfully!"
