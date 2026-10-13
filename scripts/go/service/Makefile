################################################################################
# Shared Go service dispatch
################################################################################

GO_SERVICE_DISPATCH_DIR := $(dir $(lastword $(MAKEFILE_LIST)))
GO_SERVICE_RESOLVER := $(GO_SERVICE_DISPATCH_DIR)resolve.sh
DEPLOY_ENV ?=

export SVC_NAME

.PHONY: go-svc-image-build go-svc-image-check go-svc-deploy-render go-svc-deploy-check \
	go-svc-dispatch-test

go-svc-image-build: ## Build one cataloged Go service image. Usage: make go-svc-image-build SVC_NAME=<svc-name>
	+@set -eu; \
		module_path="$$($(GO_SERVICE_RESOLVER) image-build)"; \
		$(MAKE) -C "$$module_path" image-build

go-svc-image-check: ## Build and validate one cataloged Go service image. Usage: make go-svc-image-check SVC_NAME=<svc-name>
	+@set -eu; \
		module_path="$$($(GO_SERVICE_RESOLVER) image-check)"; \
		$(MAKE) -C "$$module_path" image-check

go-svc-deploy-render: ## Render one cataloged Go service's deployment overlays. Usage: make go-svc-deploy-render SVC_NAME=<svc-name> [DEPLOY_ENV=local-dev|qa]
	+@set -eu; \
		module_path="$$($(GO_SERVICE_RESOLVER) deploy-render)"; \
		$(MAKE) --no-print-directory -C "$$module_path" deploy-render DEPLOY_ENV="$(DEPLOY_ENV)"

go-svc-deploy-check: ## Validate one cataloged Go service's isolated deployment overlays. Usage: make go-svc-deploy-check SVC_NAME=<svc-name>
	+@set -eu; \
		module_path="$$($(GO_SERVICE_RESOLVER) deploy-check)"; \
		$(MAKE) --no-print-directory -C "$$module_path" deploy-check

go-svc-dispatch-test: ## Test fail-closed Go service selection and capability dispatch
	@$(GO_SERVICE_DISPATCH_DIR)test-dispatch.sh
