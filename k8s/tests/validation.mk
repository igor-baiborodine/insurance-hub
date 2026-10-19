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
MONITORING_CHECK_TEST := $(K8S_VALIDATION_ROOT)/scripts/monitoring/check_monitoring_test.py
MONITORING_TEST_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/monitoring/test_monitoring.py
MONITORING_VERIFY_PRODUCT_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/monitoring/verify_product_observability.py
MONITORING_VERIFY_PRODUCT_TEST := $(K8S_VALIDATION_ROOT)/scripts/monitoring/verify_product_observability_test.py
MONITORING_CHECK_ENV ?= qa
MONITORING_SERVICES ?= $(notdir $(wildcard $(MONITORING_ASSETS_ROOT)/services/*))

OBSERVABILITY_BACKEND_CHECK_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/alloy/check-backend-config.py
OBSERVABILITY_BACKEND_CHECK_TEST := $(K8S_VALIDATION_ROOT)/scripts/alloy/check-backend-config-test.py
OBSERVABILITY_BACKEND_TEST_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/alloy/test-backend-ingestion.py
OBSERVABILITY_SERVICE_DOMAIN ?= product
OBSERVABILITY_SERVICE_ROOT ?= $(K8S_VALIDATION_REPOSITORY_ROOT)/services/$(OBSERVABILITY_SERVICE_DOMAIN)-service
OBSERVABILITY_SERVICE_PROBE ?= $(K8S_VALIDATION_ROOT)/scripts/alloy/probe-$(OBSERVABILITY_SERVICE_DOMAIN).py
OBSERVABILITY_REQUEST_HISTOGRAM_BUCKETS ?= 15

OBSERVABILITY_ACCEPTANCE_TEST := $(K8S_VALIDATION_ROOT)/scripts/alloy/test-disposable-acceptance-test.py
OBSERVABILITY_ACCEPTANCE_PHASE_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/alloy/test-disposable-acceptance-phase.py
OBSERVABILITY_ACCEPTANCE_PHASE_TEST := $(K8S_VALIDATION_ROOT)/scripts/alloy/test-disposable-acceptance-phase-test.py
OBSERVABILITY_ACCEPTANCE_PROFILE := $(K8S_VALIDATION_ROOT)/observability/services/product/acceptance.json
OBSERVABILITY_ACCEPTANCE_CATALOG := $(K8S_VALIDATION_REPOSITORY_ROOT)/legacy/product-service/src/test/resources/product-read-baseline/catalog/qa.json
OBSERVABILITY_ACCEPTANCE_IMAGE_TAG ?= git-$(shell git -C "$(OBSERVABILITY_SERVICE_ROOT)" rev-parse --short=12 HEAD)
OBSERVABILITY_ACCEPTANCE_IMAGE ?= insurance-hub-product-api:$(OBSERVABILITY_ACCEPTANCE_IMAGE_TAG)
OBSERVABILITY_ACCEPTANCE_EVIDENCE_DIR ?= $(K8S_VALIDATION_ROOT)/tmp/observability-acceptance
OBSERVABILITY_ACCEPTANCE_SMOKE_EVIDENCE_DIR ?= $(K8S_VALIDATION_ROOT)/tmp/observability-acceptance-smoke

ALLOY_IMAGE := grafana/alloy:v1.20.1
ALLOY_CHECK_SCRIPT := $(K8S_VALIDATION_ROOT)/scripts/alloy/check-config.py
ALLOY_CHECK_TEST := $(K8S_VALIDATION_ROOT)/scripts/alloy/check-config-test.py

.PHONY: postgres-svc-role-verify postgres-svc-role-test
postgres-svc-role-verify: ## Verify a selected service-owned database role profile. Requires explicit target coordinates.
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(K8S_VALIDATION_REPOSITORY_ROOT)/scripts/postgres/service/dispatch.py" verify

postgres-svc-role-test: ## Exercise role dispatch and Product reader lifecycle against disposable PostgreSQL
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(K8S_VALIDATION_REPOSITORY_ROOT)/scripts/postgres/service/test_reader.py"

.PHONY: prometheus-receiver-config-check
prometheus-receiver-config-check: ## Validate the pinned remote-write receiver and disabled native OTLP route without applying
	@command -v helm >/dev/null || { echo "ERROR: helm is required."; exit 1; }
	@command -v python3 >/dev/null || { echo "ERROR: Python 3.9 or newer is required."; exit 1; }
	@python3 -c 'import sys; raise SystemExit(sys.version_info < (3, 9))' || { \
		echo "ERROR: Python 3.9 or newer is required."; exit 1; \
	}
	@helm template qa-prometheus prometheus-community/kube-prometheus-stack \
		--version "$(KUBE_PROMETHEUS_STACK_VERSION)" \
		--namespace qa-monitoring \
		--values "$(PROMETHEUS_VALUES_PATH)" \
		--show-only templates/prometheus/prometheus.yaml | \
		PYTHONDONTWRITEBYTECODE=1 python3 "$(OBSERVABILITY_BACKEND_CHECK_SCRIPT)" \
			--values "$(PROMETHEUS_VALUES_PATH)" \
			--makefile "$(K8S_MAKEFILE_DIR)Makefile" \
			--makefile "$(K8S_VALIDATION_MAKEFILE)" \
			--rendered-prometheus -
	@helm template qa-prometheus prometheus-community/kube-prometheus-stack \
		--version "$(KUBE_PROMETHEUS_STACK_VERSION)" \
		--namespace qa-monitoring \
		--values "$(PROMETHEUS_VALUES_PATH)" >/dev/null
	@echo "✅ Prometheus chart $(KUBE_PROMETHEUS_STACK_VERSION) receiver configuration passed."

.PHONY: monitoring-assets-check
monitoring-assets-check: _monitoring-assets-args-check ## Validate one service dashboard and alert set without applying (ENV_NAME, SVC_NAME)
	@command -v helm >/dev/null || { echo "ERROR: helm is required."; exit 1; }
	@command -v docker >/dev/null || { echo "ERROR: docker is required."; exit 1; }
	@docker image inspect "$(PROMETHEUS_IMAGE)" >/dev/null 2>&1 || { \
		echo "ERROR: Missing pinned image $(PROMETHEUS_IMAGE). Pull it explicitly before validation."; \
		exit 1; \
	}
	@set -eu; \
	eval "$$(PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_RESOLVE_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)")"; \
	tmp_dir=$$(mktemp -d); \
	trap 'rm -rf "$$tmp_dir"' EXIT INT TERM; \
	chmod 755 "$$tmp_dir"; \
	helm template "$(ENV_NAME)-prometheus" prometheus-community/kube-prometheus-stack \
		--version "$(KUBE_PROMETHEUS_STACK_VERSION)" \
		--namespace "$$MONITORING_NAMESPACE" \
		--values "$$MONITORING_PROMETHEUS_VALUES_PATH" > "$$tmp_dir/rendered.yaml"; \
	PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_CHECK_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)" \
		--values "$$MONITORING_PROMETHEUS_VALUES_PATH" \
		--makefile "$(K8S_MAKEFILE_DIR)Makefile" \
		--makefile "$(K8S_VALIDATION_MAKEFILE)" \
		--rendered-stack "$$tmp_dir/rendered.yaml"; \
	docker run --rm \
		--mount "type=bind,src=$$MONITORING_SERVICE_DIR,dst=/assets,readonly" \
		--entrypoint /bin/promtool \
		"$(PROMETHEUS_IMAGE)" check rules "/assets/$$(basename "$$MONITORING_ALERTS_PATH")"; \
	docker run --rm \
		--mount "type=bind,src=$$MONITORING_SERVICE_DIR,dst=/fixture,readonly" \
		--entrypoint /bin/promtool \
		"$(PROMETHEUS_IMAGE)" test rules "/fixture/$$(basename "$$MONITORING_ALERT_TESTS_PATH")"
	@echo "✅ $(SVC_NAME) monitoring assets for $(ENV_NAME) passed chart $(KUBE_PROMETHEUS_STACK_VERSION) and Prometheus v3.15.0 checks."

.PHONY: monitoring-tooling-test
monitoring-tooling-test: ## Exercise generic dashboard/rule rendering, fixture, and cleanup policies
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_CHECK_TEST)" -v

.PHONY: observability-verify-test-tooling
observability-verify-test-tooling: ## Exercise Product live-verification guards and owned-forward cleanup
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_VERIFY_PRODUCT_TEST)" -v

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
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_VERIFY_PRODUCT_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)" \
		--mode "$(VERIFY_MODE)" \
		--source-revision "$(VERIFY_SOURCE_REVISION)"

.PHONY: test-monitoring
test-monitoring: _monitoring-assets-args-check ## Query one service dashboard against collected disposable Prometheus data (ENV_NAME, SVC_NAME)
	@command -v docker >/dev/null || { echo "ERROR: docker is required."; exit 1; }
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_TEST_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)"

.PHONY: monitoring-assets-check-all
monitoring-assets-check-all: ## Validate every registered monitoring service asset set
	@if [ -z "$(strip $(MONITORING_SERVICES))" ]; then \
		echo "ERROR: No service monitoring assets were found under $(MONITORING_ASSETS_ROOT)/services."; \
		exit 1; \
	fi
	@set -eu; \
	for service in $(MONITORING_SERVICES); do \
		$(MAKE) --no-print-directory monitoring-assets-check \
			ENV_NAME="$(MONITORING_CHECK_ENV)" SVC_NAME="$$service"; \
	done

.PHONY: observability-backend-test-tooling
observability-backend-test-tooling: ## Exercise receiver, fixture, query, and cleanup policy checks
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(OBSERVABILITY_BACKEND_CHECK_TEST)" -v

.PHONY: observability-acceptance-test-tooling
observability-acceptance-test-tooling: ## Exercise fixed-budget and controlled-defect acceptance checks
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(OBSERVABILITY_ACCEPTANCE_TEST)" -v
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(OBSERVABILITY_ACCEPTANCE_PHASE_TEST)" -v

.PHONY: test-observability-backends
test-observability-backends: ## Prove synthetic and real service signals in disposable pinned backends
	@command -v docker >/dev/null || { echo "ERROR: docker is required."; exit 1; }
	@command -v go >/dev/null || { echo "ERROR: Go is required for the real service probe."; exit 1; }
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(OBSERVABILITY_BACKEND_TEST_SCRIPT)" \
		--config "$(ALLOY_CONFIG_PATH)" \
		--service-domain "$(OBSERVABILITY_SERVICE_DOMAIN)" \
		--service-root "$(OBSERVABILITY_SERVICE_ROOT)" \
		--service-probe "$(OBSERVABILITY_SERVICE_PROBE)" \
		--request-histogram-buckets "$(OBSERVABILITY_REQUEST_HISTOGRAM_BUCKETS)"

define run-observability-acceptance-phase
PYTHONDONTWRITEBYTECODE=1 python3 "$(OBSERVABILITY_ACCEPTANCE_PHASE_SCRIPT)" --phase "$(1)" --config "$(ALLOY_CONFIG_PATH)" --profile "$(OBSERVABILITY_ACCEPTANCE_PROFILE)" --service-root "$(OBSERVABILITY_SERVICE_ROOT)" --catalog-fixture "$(OBSERVABILITY_ACCEPTANCE_CATALOG)" --product-image "$(OBSERVABILITY_ACCEPTANCE_IMAGE)" --time-scale "$(2)" --evidence-output "$(3)/$(1).json"
endef

define aggregate-observability-acceptance-phases
PYTHONDONTWRITEBYTECODE=1 python3 "$(OBSERVABILITY_ACCEPTANCE_PHASE_SCRIPT)" --phase aggregate --config "$(ALLOY_CONFIG_PATH)" --profile "$(OBSERVABILITY_ACCEPTANCE_PROFILE)" --service-root "$(OBSERVABILITY_SERVICE_ROOT)" --catalog-fixture "$(OBSERVABILITY_ACCEPTANCE_CATALOG)" --product-image "$(OBSERVABILITY_ACCEPTANCE_IMAGE)" --time-scale "$(1)" --evidence-input "$(2)/overhead-1.json" --evidence-input "$(2)/overhead-2.json" --evidence-input "$(2)/overhead-3.json" --evidence-input "$(2)/healthy.json" --evidence-input "$(2)/recovery.json" --evidence-output "$(2)/aggregate.json"
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
alloy-config-check: ## Validate Alloy source injection, pinned config, render, and scoped RBAC without applying
	@command -v helm >/dev/null || { echo "ERROR: helm is required."; exit 1; }
	@command -v docker >/dev/null || { echo "ERROR: docker is required."; exit 1; }
	@command -v python3 >/dev/null || { echo "ERROR: Python 3.9 or newer is required."; exit 1; }
	@python3 -c 'import sys; raise SystemExit(sys.version_info < (3, 9))' || { \
		echo "ERROR: Python 3.9 or newer is required."; exit 1; \
	}
	@docker image inspect "$(ALLOY_IMAGE)" >/dev/null 2>&1 || { \
		echo "ERROR: Missing pinned image $(ALLOY_IMAGE). Pull it explicitly before validation."; \
		exit 1; \
	}
	@helm template qa-alloy grafana/alloy \
		--version "$(ALLOY_HELM_CHART_VERSION)" \
		--namespace qa-monitoring \
		--values "$(ALLOY_VALUES_PATH)" \
		--set-file alloy.configMap.content="$(ALLOY_CONFIG_PATH)" \
		--show-only templates/rbac.yaml | \
		PYTHONDONTWRITEBYTECODE=1 python3 "$(ALLOY_CHECK_SCRIPT)" \
			--values "$(ALLOY_VALUES_PATH)" \
			--config "$(ALLOY_CONFIG_PATH)" \
			--makefile "$(K8S_MAKEFILE_DIR)Makefile" \
			--makefile "$(K8S_VALIDATION_MAKEFILE)" \
			--rendered-rbac -
	@helm template qa-alloy grafana/alloy \
		--version "$(ALLOY_HELM_CHART_VERSION)" \
		--namespace qa-monitoring \
		--values "$(ALLOY_VALUES_PATH)" \
		--set-file alloy.configMap.content="$(ALLOY_CONFIG_PATH)" \
		--show-only templates/configmap.yaml | \
		awk 'copy { sub(/^    /, ""); print } /^  config\.alloy: \|-$$/ { copy = 1 }' | \
		cmp -s - "$(ALLOY_CONFIG_PATH)" || { \
			echo "ERROR: Rendered Alloy ConfigMap differs from $(ALLOY_CONFIG_PATH)."; \
			exit 1; \
		}
	@docker run --rm -i "$(ALLOY_IMAGE)" validate /dev/stdin < "$(ALLOY_CONFIG_PATH)"
	@helm template qa-alloy grafana/alloy \
		--version "$(ALLOY_HELM_CHART_VERSION)" \
		--namespace qa-monitoring \
		--values "$(ALLOY_VALUES_PATH)" \
		--set-file alloy.configMap.content="$(ALLOY_CONFIG_PATH)" >/dev/null
	@echo "✅ Alloy chart $(ALLOY_HELM_CHART_VERSION) and $(ALLOY_IMAGE) configuration passed."

.PHONY: alloy-test-tooling
alloy-test-tooling: ## Exercise shared Alloy identity, allowlist, target, position, and RBAC fixtures
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(ALLOY_CHECK_TEST)" -v

.PHONY: check-observability
check-observability: prometheus-receiver-config-check monitoring-tooling-test observability-verify-test-tooling monitoring-assets-check-all alloy-config-check alloy-test-tooling observability-backend-test-tooling observability-acceptance-test-tooling ## Run non-mutating observability configuration and policy checks
