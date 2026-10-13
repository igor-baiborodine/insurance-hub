################################################################################
# Shared Go service dispatch
################################################################################

GO_SERVICE_DISPATCH_DIR := $(dir $(lastword $(MAKEFILE_LIST)))
GO_SERVICE_RESOLVER := $(GO_SERVICE_DISPATCH_DIR)resolve.sh

export SVC_NAME

.PHONY: go-svc-image-build go-svc-image-check go-svc-dispatch-test

go-svc-image-build: ## Build one cataloged Go service image. Usage: make go-svc-image-build SVC_NAME=<svc-name>
	+@set -eu; \
		module_path="$$($(GO_SERVICE_RESOLVER) image-build)"; \
		$(MAKE) -C "$$module_path" image-build

go-svc-image-check: ## Build and validate one cataloged Go service image. Usage: make go-svc-image-check SVC_NAME=<svc-name>
	+@set -eu; \
		module_path="$$($(GO_SERVICE_RESOLVER) image-check)"; \
		$(MAKE) -C "$$module_path" image-check

go-svc-dispatch-test: ## Test fail-closed Go service selection and image capability dispatch
	@$(GO_SERVICE_DISPATCH_DIR)test-dispatch.sh
