################################################################################
# Shared Go service dispatch
################################################################################

GO_SERVICE_DISPATCH_DIR := $(dir $(lastword $(MAKEFILE_LIST)))
GO_SERVICE_RESOLVER := $(GO_SERVICE_DISPATCH_DIR)resolve.sh
DEPLOY_ENV ?=
RELEASE_AFTER ?=
RELEASE_BEFORE ?=
RELEASE_OUTPUT ?=
RELEASE_REPOSITORY ?=
RELEASE_SERVICE ?=
RELEASE_VERSION ?=

export SVC_NAME

.PHONY: go-svc-image-build go-svc-image-check go-svc-deploy-render go-svc-deploy-check \
	go-svc-deploy-preflight go-svc-deploy-status go-svc-image-load go-svc-deploy \
	go-svc-deploy-verify go-svc-stop go-svc-deploy-test-tooling go-svc-deploy-test \
	go-svc-dispatch-test go-svc-release-select go-svc-release-update-qa go-svc-release-test

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

# The service Makefile owns resource identity and environment policy. Resolve the
# catalog capability before invoking it, so unsupported services never reach kubectl.
go-svc-deploy-preflight: ## Check exact target, ownership, image and resource prerequisites
go-svc-deploy-status: ## Observe selected Go and Java workload status and exposure
go-svc-image-load: ## Load the selected image into local-dev Kind
go-svc-deploy: ## Apply the selected local-dev Go overlay with bounded rollout
go-svc-deploy-verify: ## Verify selected Go resources, reader permissions and HTTP/gRPC reads
go-svc-stop: ## Stop only the selected local-dev Go Deployment
go-svc-deploy-test-tooling: ## Test selected-service deployment guards and cleanup
go-svc-deploy-test: ## Exercise one service in an owned disposable Kind namespace
go-svc-deploy-preflight go-svc-deploy-status go-svc-image-load go-svc-deploy \
	go-svc-deploy-verify go-svc-stop go-svc-deploy-test-tooling go-svc-deploy-test:
	+@set -eu; \
		capability='$(@:go-svc-%=%)'; \
		module_path="$$($(GO_SERVICE_RESOLVER) "$$capability")"; \
		$(MAKE) --no-print-directory -C "$$module_path" "$$capability"

go-svc-dispatch-test: ## Test fail-closed Go service selection and capability dispatch
	@$(GO_SERVICE_DISPATCH_DIR)test-dispatch.sh

go-svc-release-select: ## Select exactly one changed conventional Go service for release
	@PYTHONDONTWRITEBYTECODE=1 python3 $(GO_SERVICE_DISPATCH_DIR)release.py select \
		--before "$(RELEASE_BEFORE)" --after "$(RELEASE_AFTER)" \
		--repository "$(RELEASE_REPOSITORY)" --output "$(RELEASE_OUTPUT)"

go-svc-release-update-qa: ## Update the selected Go service QA overlay to a semantic release tag
	@PYTHONDONTWRITEBYTECODE=1 python3 $(GO_SERVICE_DISPATCH_DIR)release.py update-qa \
		--service "$(RELEASE_SERVICE)" --repository "$(RELEASE_REPOSITORY)" \
		--version "$(RELEASE_VERSION)"

go-svc-release-test: ## Test generic Go service selection and QA semantic-tag updates
	@PYTHONDONTWRITEBYTECODE=1 python3 $(GO_SERVICE_DISPATCH_DIR)test-release.py
