################################################################################
# Kubernetes test and verification targets
# Reusable fragment included explicitly by the repository root Makefile.
################################################################################

################################################################################
# Test and verification variables
################################################################################

K8S_VALIDATION_MAKEFILE := $(abspath $(lastword $(MAKEFILE_LIST)))
K8S_VALIDATION_DIR := $(dir $(K8S_VALIDATION_MAKEFILE))
K8S_VALIDATION_ROOT := $(abspath $(K8S_VALIDATION_DIR)..)
K8S_VALIDATION_REPOSITORY_ROOT := $(abspath $(K8S_VALIDATION_ROOT)/..)

PROMETHEUS_IMAGE := quay.io/prometheus/prometheus:v3.15.0-distroless

MONITORING_CHECK_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/monitoring/check_monitoring.py
MONITORING_CHECK_TEST := $(K8S_VALIDATION_ROOT)/scripts/monitoring/test_check_monitoring.py
MONITORING_TEST_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/monitoring/monitoring_integration.py
MONITORING_VERIFY_PRODUCT_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/monitoring/verify_product_observability.py
MONITORING_VERIFY_PRODUCT_TEST := $(K8S_VALIDATION_ROOT)/scripts/monitoring/test_verify_product_observability.py
MONITORING_CHECK_ENV ?= qa
MONITORING_SERVICES ?= $(notdir $(wildcard $(MONITORING_ASSETS_ROOT)/services/*))

OBSERVABILITY_BACKEND_CHECK_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/alloy/check_backend_config.py
OBSERVABILITY_BACKEND_CHECK_TEST := $(K8S_VALIDATION_ROOT)/scripts/alloy/test_check_backend_config.py
OBSERVABILITY_BACKEND_TEST_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/alloy/backend_ingestion.py
OBSERVABILITY_SERVICE_DOMAIN ?= product
OBSERVABILITY_SERVICE_ROOT ?= $(K8S_VALIDATION_REPOSITORY_ROOT)/services/$(OBSERVABILITY_SERVICE_DOMAIN)-service
OBSERVABILITY_SERVICE_PROBE ?= $(K8S_VALIDATION_ROOT)/scripts/alloy/probe_$(OBSERVABILITY_SERVICE_DOMAIN).py
OBSERVABILITY_REQUEST_HISTOGRAM_BUCKETS ?= 15

OBSERVABILITY_ACCEPTANCE_TEST := $(K8S_VALIDATION_ROOT)/scripts/alloy/test_disposable_acceptance.py
OBSERVABILITY_ACCEPTANCE_PHASE_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/alloy/disposable_acceptance_phase.py
OBSERVABILITY_ACCEPTANCE_PHASE_TEST := $(K8S_VALIDATION_ROOT)/scripts/alloy/test_disposable_acceptance_phase.py
OBSERVABILITY_ACCEPTANCE_PROFILE := $(K8S_VALIDATION_ROOT)/observability/services/product/acceptance.json
OBSERVABILITY_ACCEPTANCE_CATALOG := $(K8S_VALIDATION_REPOSITORY_ROOT)/legacy/product-service/src/test/resources/product-read-baseline/catalog/qa.json
OBSERVABILITY_ACCEPTANCE_IMAGE_TAG ?= git-$(shell git -C "$(OBSERVABILITY_SERVICE_ROOT)" rev-parse --short=12 HEAD)
OBSERVABILITY_ACCEPTANCE_IMAGE ?= insurance-hub-product-api:$(OBSERVABILITY_ACCEPTANCE_IMAGE_TAG)
OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR ?= $(K8S_VALIDATION_ROOT)/tmp/observability-acceptance
OBSERVABILITY_ACCEPTANCE_SMOKE_EVIDENCE_DIR ?= $(K8S_VALIDATION_ROOT)/tmp/observability-acceptance-smoke

ALLOY_IMAGE := grafana/alloy:v1.20.1
ALLOY_CHECK_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/alloy/check_config.py
ALLOY_CHECK_TEST := $(K8S_VALIDATION_ROOT)/scripts/alloy/test_check_config.py

.PHONY: postgres-svc-role-verify postgres-svc-role-test-fast postgres-svc-role-test
postgres-svc-role-verify: python-runtime-check ## Verify a selected service-owned database role profile. Requires explicit target coordinates.
	@PYTHONDONTWRITEBYTECODE=1 $(PYTHON) "$(K8S_VALIDATION_REPOSITORY_ROOT)/scripts/postgres/service/dispatch.py" verify

postgres-svc-role-test-fast: python-toolchain-check ## Exercise Product role guards and adapters without a live target
	@cd "$(K8S_VALIDATION_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -m pytest -q \
		-m 'not integration and not acceptance' scripts/postgres/service/test_reader.py \
		scripts/postgres/service/test_administration_roles.py

postgres-svc-role-test: python-toolchain-check ## Exercise Product role lifecycle against disposable PostgreSQL
	@cd "$(K8S_VALIDATION_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -m pytest -q \
		scripts/postgres/service/test_reader.py scripts/postgres/service/test_administration_roles.py

.PHONY: prometheus-receiver-config-check
prometheus-receiver-config-check: python-runtime-check ## Validate the pinned remote-write receiver and disabled native OTLP route without applying
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(MONITORING_OPERATIONS_SCRIPT)" receiver-check \
		--stack-version "$(KUBE_PROMETHEUS_STACK_VERSION)" --prometheus-values "$(PROMETHEUS_VALUES_PATH)"

.PHONY: monitoring-assets-check
monitoring-assets-check: _monitoring-assets-args-check ## Validate one service dashboard and alert set without applying (ENV_NAME, SVC_NAME)
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(MONITORING_OPERATIONS_SCRIPT)" assets-check \
		--assets-root "$(MONITORING_ASSETS_ROOT)" --environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)" --stack-version "$(KUBE_PROMETHEUS_STACK_VERSION)" \
		--prometheus-image "$(PROMETHEUS_IMAGE)"

.PHONY: monitoring-tooling-test
monitoring-tooling-test: python-toolchain-check ## Exercise generic dashboard/rule rendering, fixture, and cleanup policies
	@cd "$(K8S_VALIDATION_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 \
		PYTHONPATH="$(K8S_VALIDATION_ROOT)/scripts/monitoring" "$(PYTHON)" -m pytest -q \
		-m 'not integration and not acceptance' "$(MONITORING_CHECK_TEST)"

.PHONY: observability-verify-test-tooling
observability-verify-test-tooling: python-toolchain-check ## Exercise Product live-verification guards and owned-forward cleanup
	@cd "$(K8S_VALIDATION_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 \
		PYTHONPATH="$(K8S_VALIDATION_ROOT)/scripts/monitoring" "$(PYTHON)" -m pytest -q \
		-m 'not integration and not acceptance' "$(MONITORING_VERIFY_PRODUCT_TEST)"

.PHONY: verify-product-observability
verify-product-observability: _monitoring-assets-args-check ## Read-only Product QA verification (ENV_NAME, SVC_NAME, VERIFY_MODE, VERIFY_SOURCE_REVISION)
	@if [ "$(origin VERIFY_MODE)" != "command line" ] || \
		{ [ "$(VERIFY_MODE)" != "disabled" ] && [ "$(VERIFY_MODE)" != "enabled" ]; }; then \
		echo "ERROR: VERIFY_MODE=disabled|enabled is required on the make command line."; \
		exit 1; \
	fi
	@if [ "$(origin VERIFY_SOURCE_REVISION)" != "command line" ] || \
		! printf '%s' "$(VERIFY_SOURCE_REVISION)" | grep -Eq '^[0-9a-f]{40}$$'; then \
		echo "ERROR: VERIFY_SOURCE_REVISION=<40-lowercase-hex> is required on the make command line."; \
		exit 1; \
	fi
	@command -v kubectl >/dev/null || { echo "ERROR: kubectl is required."; exit 1; }
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(MONITORING_VERIFY_PRODUCT_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)" \
		--mode "$(VERIFY_MODE)" \
		--source-revision "$(VERIFY_SOURCE_REVISION)"

.PHONY: test-monitoring
test-monitoring: _monitoring-assets-args-check ## Query one service dashboard against collected disposable Prometheus data (ENV_NAME, SVC_NAME)
	@command -v docker >/dev/null || { echo "ERROR: docker is required."; exit 1; }
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(MONITORING_TEST_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)"

.PHONY: monitoring-assets-check-all
monitoring-assets-check-all: python-runtime-check ## Validate every registered monitoring service asset set
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(MONITORING_OPERATIONS_SCRIPT)" assets-check-all \
		--assets-root "$(MONITORING_ASSETS_ROOT)" --environment "$(MONITORING_CHECK_ENV)" \
		--services $(MONITORING_SERVICES) --stack-version "$(KUBE_PROMETHEUS_STACK_VERSION)" \
		--prometheus-image "$(PROMETHEUS_IMAGE)"

.PHONY: observability-backend-test-tooling
observability-backend-test-tooling: python-toolchain-check ## Exercise receiver, fixture, query, and cleanup policy checks
	@cd "$(K8S_VALIDATION_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 \
		PYTHONPATH="$(K8S_VALIDATION_ROOT)/scripts/alloy" "$(PYTHON)" -m pytest -q \
		-m 'not integration and not acceptance' "$(OBSERVABILITY_BACKEND_CHECK_TEST)"

.PHONY: observability-acceptance-test-tooling
observability-acceptance-test-tooling: python-toolchain-check ## Exercise fixed-budget and controlled-defect acceptance checks
	@cd "$(K8S_VALIDATION_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 \
		PYTHONPATH="$(K8S_VALIDATION_ROOT)/scripts/alloy" "$(PYTHON)" -m pytest -q \
		-m 'not integration and not acceptance' \
		"$(OBSERVABILITY_ACCEPTANCE_TEST)" "$(OBSERVABILITY_ACCEPTANCE_PHASE_TEST)"

.PHONY: test-observability-backends
test-observability-backends: python-runtime-check ## Prove synthetic and real service signals in disposable pinned backends
	@command -v docker >/dev/null || { echo "ERROR: docker is required."; exit 1; }
	@command -v go >/dev/null || { echo "ERROR: Go is required for the real service probe."; exit 1; }
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(OBSERVABILITY_BACKEND_TEST_SCRIPT)" \
		--config "$(ALLOY_CONFIG_PATH)" \
		--service-domain "$(OBSERVABILITY_SERVICE_DOMAIN)" \
		--service-root "$(OBSERVABILITY_SERVICE_ROOT)" \
		--service-probe "$(OBSERVABILITY_SERVICE_PROBE)" \
		--request-histogram-buckets "$(OBSERVABILITY_REQUEST_HISTOGRAM_BUCKETS)"

define run-observability-acceptance-phase
PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(OBSERVABILITY_ACCEPTANCE_PHASE_SCRIPT)" --phase "$(1)" --config "$(ALLOY_CONFIG_PATH)" --profile "$(OBSERVABILITY_ACCEPTANCE_PROFILE)" --service-root "$(OBSERVABILITY_SERVICE_ROOT)" --catalog-fixture "$(OBSERVABILITY_ACCEPTANCE_CATALOG)" --product-image "$(OBSERVABILITY_ACCEPTANCE_IMAGE)" --time-scale "$(2)" --evidence-output "$(3)/$(1).json"
endef

define aggregate-observability-acceptance-phases
PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(OBSERVABILITY_ACCEPTANCE_PHASE_SCRIPT)" --phase aggregate --config "$(ALLOY_CONFIG_PATH)" --profile "$(OBSERVABILITY_ACCEPTANCE_PROFILE)" --service-root "$(OBSERVABILITY_SERVICE_ROOT)" --catalog-fixture "$(OBSERVABILITY_ACCEPTANCE_CATALOG)" --product-image "$(OBSERVABILITY_ACCEPTANCE_IMAGE)" --time-scale "$(1)" --evidence-input "$(2)/overhead-1.json" --evidence-input "$(2)/overhead-2.json" --evidence-input "$(2)/overhead-3.json" --evidence-input "$(2)/healthy.json" --evidence-input "$(2)/recovery.json" --evidence-output "$(2)/aggregate.json"
endef

.PHONY: test-observability-acceptance-preflight
test-observability-acceptance-preflight: observability-acceptance-test-tooling test-observability-backends ## Build and validate the fixed acceptance candidate before phased execution
	@command -v docker >/dev/null || { echo "ERROR: docker is required."; exit 1; }
	@command -v go >/dev/null || { echo "ERROR: Go is required for the Product acceptance helper."; exit 1; }
	@$(MAKE) --no-print-directory -C "$(OBSERVABILITY_SERVICE_ROOT)" image-build \
		IMAGE_TAG="$(OBSERVABILITY_ACCEPTANCE_IMAGE_TAG)"
	@$(MAKE) --no-print-directory -C "$(OBSERVABILITY_SERVICE_ROOT)" test \
		TEST_PACKAGES='./internal/telemetry ./internal/logger'
	@$(MAKE) --no-print-directory -C "$(OBSERVABILITY_SERVICE_ROOT)" test-telemetry-continuity
	@$(MAKE) --no-print-directory monitoring-assets-check test-monitoring \
		ENV_NAME=qa SVC_NAME=product

.PHONY: test-observability-acceptance-overhead-1
test-observability-acceptance-overhead-1: test-observability-acceptance-preflight ## Run acceptance overhead pair 1 (disabled, then enabled)
	@mkdir -p "$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR)"
	@$(call run-observability-acceptance-phase,overhead-1,1,$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR))

.PHONY: test-observability-acceptance-overhead-2
test-observability-acceptance-overhead-2: test-observability-acceptance-preflight ## Run acceptance overhead pair 2 (enabled, then disabled)
	@mkdir -p "$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR)"
	@$(call run-observability-acceptance-phase,overhead-2,1,$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR))

.PHONY: test-observability-acceptance-overhead-3
test-observability-acceptance-overhead-3: test-observability-acceptance-preflight ## Run acceptance overhead pair 3 (disabled, then enabled)
	@mkdir -p "$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR)"
	@$(call run-observability-acceptance-phase,overhead-3,1,$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR))

.PHONY: test-observability-acceptance-healthy
test-observability-acceptance-healthy: test-observability-acceptance-preflight ## Run the paired Alloy baseline and healthy Product workload phase
	@mkdir -p "$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR)"
	@$(call run-observability-acceptance-phase,healthy,1,$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR))

.PHONY: test-observability-acceptance-recovery
test-observability-acceptance-recovery: test-observability-acceptance-preflight ## Run freshness, safety, outage, replacement, and recovery acceptance
	@mkdir -p "$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR)"
	@$(call run-observability-acceptance-phase,recovery,1,$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR))

.PHONY: test-observability-acceptance-aggregate
test-observability-acceptance-aggregate: ## Validate and combine compatible acceptance phase evidence
	@$(call aggregate-observability-acceptance-phases,1,$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR))

.NOTPARALLEL: test-observability-acceptance
.PHONY: test-observability-acceptance
test-observability-acceptance: test-observability-acceptance-overhead-1 test-observability-acceptance-overhead-2 test-observability-acceptance-overhead-3 test-observability-acceptance-healthy test-observability-acceptance-recovery ## Run all restartable Product acceptance phases and aggregate their evidence
	@$(call aggregate-observability-acceptance-phases,1,$(OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR))

.PHONY: test-observability-acceptance-smoke
test-observability-acceptance-smoke: observability-acceptance-test-tooling ## Exercise the disposable acceptance lifecycle with reduced timing; not contract evidence
	@command -v docker >/dev/null || { echo "ERROR: docker is required."; exit 1; }
	@$(MAKE) --no-print-directory -C "$(OBSERVABILITY_SERVICE_ROOT)" image-build \
		IMAGE_TAG="$(OBSERVABILITY_ACCEPTANCE_IMAGE_TAG)"
	@mkdir -p "$(OBSERVABILITY_ACCEPTANCE_SMOKE_EVIDENCE_DIR)"
	@$(call run-observability-acceptance-phase,overhead-1,0.01,$(OBSERVABILITY_ACCEPTANCE_SMOKE_EVIDENCE_DIR))
	@$(call run-observability-acceptance-phase,overhead-2,0.01,$(OBSERVABILITY_ACCEPTANCE_SMOKE_EVIDENCE_DIR))
	@$(call run-observability-acceptance-phase,overhead-3,0.01,$(OBSERVABILITY_ACCEPTANCE_SMOKE_EVIDENCE_DIR))
	@$(call run-observability-acceptance-phase,healthy,0.01,$(OBSERVABILITY_ACCEPTANCE_SMOKE_EVIDENCE_DIR))
	@$(call run-observability-acceptance-phase,recovery,0.01,$(OBSERVABILITY_ACCEPTANCE_SMOKE_EVIDENCE_DIR))
	@$(call aggregate-observability-acceptance-phases,0.01,$(OBSERVABILITY_ACCEPTANCE_SMOKE_EVIDENCE_DIR))

.PHONY: alloy-config-check
alloy-config-check: python-runtime-check ## Validate Alloy source injection, pinned config, render, and scoped RBAC without applying
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(MONITORING_OPERATIONS_SCRIPT)" alloy-check \
		--alloy-version "$(ALLOY_HELM_CHART_VERSION)" --alloy-image "$(ALLOY_IMAGE)" \
		--alloy-values "$(ALLOY_VALUES_PATH)" --alloy-config "$(ALLOY_CONFIG_PATH)"

.PHONY: alloy-test-tooling
alloy-test-tooling: python-toolchain-check ## Exercise shared Alloy identity, allowlist, target, position, and RBAC fixtures
	@cd "$(K8S_VALIDATION_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 \
		PYTHONPATH="$(K8S_VALIDATION_ROOT)/scripts/alloy" "$(PYTHON)" -m pytest -q \
		-m 'not integration and not acceptance' "$(ALLOY_CHECK_TEST)"

.PHONY: check-observability
check-observability: prometheus-receiver-config-check monitoring-tooling-test observability-verify-test-tooling monitoring-assets-check-all alloy-config-check alloy-test-tooling observability-backend-test-tooling observability-acceptance-test-tooling ## Run non-mutating observability configuration and policy checks
