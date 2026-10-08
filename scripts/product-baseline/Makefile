################################################################################
# Product read baseline targets
################################################################################

BASELINE_ENV ?=
BASELINE_PART ?=
BASELINE_SUITE ?=
BASELINE_FIXTURE_SET ?=
BASELINE_PRODUCT_DB_HOST ?=
BASELINE_PRODUCT_DB_PORT ?=
BASELINE_PRODUCT_DB_AFTER_PORT ?=
BASELINE_PRODUCT_URL ?=
BASELINE_GATEWAY_URL ?=
BASELINE_GATEWAY_TOKEN_FILE ?=

export BASELINE_ENV
export BASELINE_PART
export BASELINE_SUITE
export BASELINE_FIXTURE_SET
export BASELINE_PRODUCT_DB_HOST
export BASELINE_PRODUCT_DB_PORT
export BASELINE_PRODUCT_DB_AFTER_PORT
export BASELINE_PRODUCT_URL
export BASELINE_GATEWAY_URL
export BASELINE_GATEWAY_TOKEN_FILE

.PHONY: product-baseline-preflight product-baseline-capture product-baseline-test \
	product-baseline-fixtures-check product-baseline-replay product-baseline-verify \
	product-baseline-check _product-baseline-resolve-java _product-baseline-test-smoke \
	_product-baseline-test-happy _product-baseline-test-data-edges _product-baseline-test-failures \
	_product-baseline-test-access

product-baseline-preflight: ## Check read-only Product baseline prerequisites for one explicit environment
	@./scripts/product-baseline/preflight.sh

product-baseline-capture: ## Capture a Product baseline part with BASELINE_ENV=<environment> BASELINE_PART=<part>
	@./scripts/product-baseline/capture.sh

product-baseline-test: ## Run a named disposable Product baseline suite with BASELINE_SUITE=<suite>
	@./scripts/product-baseline/validate-suite.sh
ifeq ($(BASELINE_SUITE),smoke)
	+@$(MAKE) --no-print-directory _product-baseline-test-smoke
else ifeq ($(BASELINE_SUITE),happy)
	+@$(MAKE) --no-print-directory _product-baseline-test-happy
else ifeq ($(BASELINE_SUITE),data-edges)
	+@$(MAKE) --no-print-directory _product-baseline-test-data-edges
else ifeq ($(BASELINE_SUITE),failures)
	+@$(MAKE) --no-print-directory _product-baseline-test-failures
else ifeq ($(BASELINE_SUITE),access)
	+@$(MAKE) --no-print-directory _product-baseline-test-access
else
	@./scripts/product-baseline/not-implemented.sh test-suite BASELINE_SUITE
endif

product-baseline-fixtures-check: ## Check Product fixtures, optionally filtered with BASELINE_ENV and BASELINE_PART
	@./scripts/product-baseline/check-fixtures.sh

product-baseline-replay: ## Replay an accepted Product fixture set (implemented in a later delivery step)
	@./scripts/product-baseline/not-implemented.sh replay BASELINE_FIXTURE_SET

product-baseline-verify: ## Verify a live Product environment (implemented in a later delivery step)
	@./scripts/product-baseline/not-implemented.sh verify BASELINE_ENV

product-baseline-check: ## Run the complete Product baseline gate (implemented in a later delivery step)
	@./scripts/product-baseline/not-implemented.sh check

_product-baseline-resolve-java:
	@./scripts/product-baseline/resolve-java.sh

_product-baseline-test-smoke: _product-baseline-resolve-java
	@./scripts/product-baseline/test-tooling.sh
	@./scripts/product-baseline/test-smoke.sh

_product-baseline-test-happy: _product-baseline-resolve-java
	@./scripts/product-baseline/test-happy.sh

_product-baseline-test-data-edges: _product-baseline-resolve-java
	@./scripts/product-baseline/test-data-edges.sh

_product-baseline-test-failures: _product-baseline-resolve-java
	@./scripts/product-baseline/test-failures.sh

_product-baseline-test-access: _product-baseline-resolve-java
	@./scripts/product-baseline/test-access.sh
