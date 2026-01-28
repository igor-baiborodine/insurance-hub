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
	@grep -h -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2}'

################################################################################
# Go Targets
################################################################################
.PHONY: go-build
go-build: ## Compile the Go application
	@echo ">> Building $(APP_NAME) (Go $(GO_VERSION))"
	@go build -o $(BIN_DIR)/$(APP_NAME) ./...

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
	docker build -f "$$SVC_FOLDER/Dockerfile" "$$SVC_FOLDER" -t "$$IMAGE_NAME"; \
	echo "✅ Docker image '$$IMAGE_NAME' built successfully."

.PHONY: docker-frontend-build
docker-frontend-build: ## Build the Docker image for the Vue frontend in the 'legacy' folder. Usage: make docker-frontend-build
	@SVC_FOLDER="legacy/web-vue"; \
	IMAGE_NAME="insurance-hub-web-vue-legacy:latest"; \
	echo "🔨 Building Docker image for Frontend from '$$SVC_FOLDER'..."; \
	docker build --no-cache -f "$$SVC_FOLDER/Dockerfile" "$$SVC_FOLDER" -t "$$IMAGE_NAME"; \
	docker image prune -f; \
	echo "✅ Docker image '$$IMAGE_NAME' built successfully."

.PHONY: docker-all-build
docker-all-build: ## Build frontend and all Java services. Usage: make docker-all-build
	echo "🔨 Building all Insurance Hub Docker images..." ; \
	$(MAKE) docker-frontend-build

	SVC_NAMES="web-vue,agent-portal-gateway,auth,product,policy-search,dashboard,policy,pricing,document,payment" ; \
	@for svc in $${SVC_NAMES//,/ } ; do \
		$(MAKE) docker-java-svc-build SVC_NAME=$$svc ; \
	done

	@echo "✅ All Docker images built!"
	@echo "docker images | grep insurance-hub"

##############################################################################################
# Local Dev Cluster Infra and Services Deployment Targets
# Targets for deploying and managing Insurance Hub infra and service apps in local dev cluster
##############################################################################################

.PHONY: legacy-all-build
legacy-all-build: ## Build all legacy artifacts sequentially (Java → Frontend → Docker). Usage: legacy-all-build
	@echo "🚀 Building all legacy artifacts (Java → Frontend → Docker)..."

	$(MAKE) java-all-build
	$(MAKE) frontend-build
	$(MAKE) docker-all-build

	@echo "✅ Legacy all-build complete!"
	@echo "📦 Ready for k8s deployment!"

.PHONY: local-dev-infra-deploy
local-dev-infra-deploy: _env-check ## Deploy all infra apps into local dev Kind cluster. Usage: make local-dev-infra-deploy
	@echo "🚀 Deploying local-dev infra apps (all optional args omitted)..."
	@if [ "$(ENV_NAME)" != "local-dev" ]; then \
		echo "ERROR: This target is only applicable for the 'local-dev' environment."; \
		exit 1; \
	fi

	# Infra operators
	$(MAKE) es-operator-deploy & \
	$(MAKE) postgres-operator-deploy & \
	$(MAKE) minio-operator-deploy & \
	$(MAKE) mongodb-operator-install & \
	$(MAKE) kafka-strimzi-operator-install & wait
	@echo "⏳ Waiting 15s for infra operators..." && sleep 15

	# Elasticsearch
	$(MAKE) es-deploy

	# Postgres
	POSTGRES_SVC_NAMES="auth,document,payment,policy,pricing,product" ; \
	@for svc in $${POSTGRES_SVC_NAMES//,/ } ; do \
		echo "  $$svc" ; \
		$(MAKE) postgres-svc-secret-create SVC_NAME=$$svc ; \
		$(MAKE) postgres-svc-deploy SVC_NAME=$$svc ; \
	done ; \

	# MongoDB
	$(MAKE) mongodb-root-user-secret-create
	$(MAKE) mongodb-product-user-secret-create
	$(MAKE) mongodb-deploy

	# Kafka
	$(MAKE) kafka-deploy

	# MinIO Tenants
	$(MAKE) minio-storage-user-secret-create SVC_NAME=document
	$(MAKE) minio-storage-config-secret-create SVC_NAME=document
	$(MAKE) minio-storage-user-secret-create SVC_NAME=payment
	$(MAKE) minio-storage-config-secret-create SVC_NAME=payment
	$(MAKE) minio-tenant-deploy SVC_NAME=document & $(MAKE) minio-tenant-deploy SVC_NAME=payment & wait
	@echo "⏳ Waiting 3m for MinIO tenants.." && sleep 180

	# MinIO Tenant Configuration
	# Document
	$(MAKE) minio-svc-bucket-create SVC_NAME=document BUCKET_NAME=policies
	$(MAKE) minio-svc-user-secret-create SVC_NAME=document
	$(MAKE) minio-svc-user-with-policy-create SVC_NAME=document POLICY_FILE=apps/svc/document/minio/s3-policy-policies.json
	# Payment
	$(MAKE) minio-svc-bucket-create SVC_NAME=payment BUCKET_NAME=payments-import
	$(MAKE) minio-svc-user-secret-create SVC_NAME=payment
	$(MAKE) minio-svc-user-with-policy-create SVC_NAME=payment POLICY_FILE=apps/svc/payment/minio/s3-policy-payments-import.json

	@echo "✅ Infra apps deployed to local-dev cluster!"
	@kubectl get pods -A | grep -E "local-dev-all|minio|elastic|kafka|cnpg"

.PHONY: local-dev-svc-deploy
local-dev-svc-deploy:  _env-check ## Deploy all services into local dev Kind cluster. Usage: make local-dev-svc-deploy
	@echo "🚀 Deploying local-dev services..."
	@if [ "$(ENV_NAME)" != "local-dev" ]; then \
		echo "ERROR: This target is only applicable for the 'local-dev' environment."; \
		exit 1; \
	fi

	$(MAKE) jsreport-deploy

	SVC_NAMES="web-vue,agent-portal-gateway,auth,product,policy-search,dashboard,policy,pricing,document,payment" ; \
	@for svc in $${SVC_NAMES//,/ } ; do \
		$(MAKE) svc-load-delete-deploy SVC_NAME=$$svc ; \
	done
	@echo "⏳ Waiting 60s for services.." && sleep 60

	@echo "✅ Service apps deployed to local-dev cluster!"
	@kubectl get pods | grep -E "web|agent|api"
	@kubectl top pods | grep -E "web|agent|api"
