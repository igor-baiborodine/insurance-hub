################################################################################
# Shared Go service dispatch
################################################################################

GO_SERVICE_DISPATCH_DIR := $(dir $(lastword $(MAKEFILE_LIST)))
GO_SERVICE_REPOSITORY_ROOT := $(abspath $(GO_SERVICE_DISPATCH_DIR)../../..)
include $(GO_SERVICE_REPOSITORY_ROOT)/scripts/python/toolchain.mk
GO_SERVICE_RESOLVER := $(GO_SERVICE_DISPATCH_DIR)resolve.sh
DEPLOY_ENV ?=
RELEASE_AFTER ?=
RELEASE_BEFORE ?=
RELEASE_OUTPUT ?=
RELEASE_REPOSITORY ?=
RELEASE_SERVICE ?=
RELEASE_VERSION ?=
RELEASE_IMAGE_DIGEST ?=
RELEASE_SOURCE_COMMIT ?=
RELEASE_TARGET ?=

export SVC_NAME

.PHONY: go-svc-image-build go-svc-image-check go-svc-deploy-render go-svc-deploy-check \
	go-svc-deploy-preflight go-svc-deploy-status go-svc-image-load go-svc-deploy \
	go-svc-deploy-verify go-svc-stop go-svc-deploy-test-tooling go-svc-deploy-test \
	go-svc-deploy-test-flux \
	go-svc-dispatch-test go-svc-release-select go-svc-release-prepare go-svc-release-test go-svc-release-test-collect

go-svc-image-build: python-runtime-check ## Build one cataloged Go service image. Usage: make go-svc-image-build SVC_NAME=<svc-name>
	+@set -eu; \
		module_path="$$(PYTHON="$(PYTHON)" $(GO_SERVICE_RESOLVER) image-build)"; \
		$(MAKE) -C "$$module_path" image-build

go-svc-image-check: python-runtime-check ## Build and validate one cataloged Go service image. Usage: make go-svc-image-check SVC_NAME=<svc-name>
	+@set -eu; \
		module_path="$$(PYTHON="$(PYTHON)" $(GO_SERVICE_RESOLVER) image-check)"; \
		$(MAKE) -C "$$module_path" image-check

go-svc-deploy-render: python-runtime-check ## Render one cataloged Go service's deployment overlays. Usage: make go-svc-deploy-render SVC_NAME=<svc-name> [DEPLOY_ENV=<target>]
	+@set -eu; \
		module_path="$$(PYTHON="$(PYTHON)" $(GO_SERVICE_RESOLVER) deploy-render)"; \
		$(MAKE) --no-print-directory -C "$$module_path" deploy-render DEPLOY_ENV="$(DEPLOY_ENV)"

go-svc-deploy-check: python-runtime-check ## Validate one cataloged Go service's isolated deployment overlays. Usage: make go-svc-deploy-check SVC_NAME=<svc-name>
	+@set -eu; \
		module_path="$$(PYTHON="$(PYTHON)" $(GO_SERVICE_RESOLVER) deploy-check)"; \
		$(MAKE) --no-print-directory -C "$$module_path" deploy-check

# The service Makefile owns resource identity and environment policy. Resolve the
# catalog capability before invoking it, so unsupported services never reach kubectl.
go-svc-deploy-preflight: ## Check exact target, ownership, image and resource prerequisites
go-svc-deploy-status: ## Observe selected Go workload status, Java absence, and exposure
go-svc-image-load: ## Load the selected image into its configured Kind target
go-svc-deploy: ## Apply the selected direct target with bounded rollout
go-svc-deploy-verify: ## Verify selected Go resources, reader permissions and HTTP/gRPC reads
go-svc-stop: ## Stop only the selected direct-target Deployment
go-svc-deploy-test-tooling: ## Test selected-service deployment guards and cleanup
go-svc-deploy-test: ## Exercise one service in an owned disposable Kind namespace
go-svc-deploy-test-flux: ## Exercise one service with pinned Flux/CNPG in an owned Kind cluster
go-svc-deploy-preflight go-svc-deploy-status go-svc-image-load go-svc-deploy \
	go-svc-deploy-verify go-svc-stop go-svc-deploy-test-tooling go-svc-deploy-test \
	go-svc-deploy-test-flux: python-runtime-check
	+@set -eu; \
		capability='$(@:go-svc-%=%)'; \
		module_path="$$(PYTHON="$(PYTHON)" $(GO_SERVICE_RESOLVER) "$$capability")"; \
		$(MAKE) --no-print-directory -C "$$module_path" "$$capability"

go-svc-dispatch-test: python-toolchain-check ## Test fail-closed Go service selection and capability dispatch
	@PYTHON="$(PYTHON)" $(GO_SERVICE_DISPATCH_DIR)test-dispatch.sh

go-svc-release-select: python-runtime-check ## Select exactly one changed conventional Go service for release
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" $(GO_SERVICE_DISPATCH_DIR)release.py select \
		--before "$(RELEASE_BEFORE)" --after "$(RELEASE_AFTER)" \
		--repository "$(RELEASE_REPOSITORY)" --output "$(RELEASE_OUTPUT)"

go-svc-release-prepare: python-runtime-check ## Prepare catalog-selected release targets and emit an exact NUL-delimited staging list
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" $(GO_SERVICE_DISPATCH_DIR)release.py prepare \
		--service "$(RELEASE_SERVICE)" --repository "$(RELEASE_REPOSITORY)" \
		--version "$(RELEASE_VERSION)" --source-commit "$(RELEASE_SOURCE_COMMIT)" \
		--image-digest "$(RELEASE_IMAGE_DIGEST)" --target "$(RELEASE_TARGET)" \
		--output "$(RELEASE_OUTPUT)"

go-svc-release-test: python-toolchain-check ## Test generic Go service selection and catalog-selected release preparation
	@cd "$(GO_SERVICE_DISPATCH_DIR)" && PYTHONDONTWRITEBYTECODE=1 \
		"$(PYTHON)" -m pytest -q test_release.py test_deployment.py

go-svc-release-test-collect: python-toolchain-check ## Collect shared release and deployment contracts without execution
	@cd "$(GO_SERVICE_DISPATCH_DIR)" && PYTHONDONTWRITEBYTECODE=1 \
		"$(PYTHON)" -m pytest --collect-only -q test_release.py test_deployment.py
