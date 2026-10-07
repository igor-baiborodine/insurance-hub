################################################################################
# Product read baseline targets
################################################################################

BASELINE_ENV ?=
BASELINE_PART ?=
BASELINE_SUITE ?=
BASELINE_FIXTURE_SET ?=
BASELINE_PRODUCT_DB_HOST ?=
BASELINE_PRODUCT_DB_PORT ?=

export BASELINE_ENV
export BASELINE_PART
export BASELINE_SUITE
export BASELINE_FIXTURE_SET
export BASELINE_PRODUCT_DB_HOST
export BASELINE_PRODUCT_DB_PORT

.PHONY: product-baseline-preflight product-baseline-capture product-baseline-test \
	product-baseline-fixtures-check product-baseline-replay product-baseline-verify \
	product-baseline-check _product-baseline-resolve-java _product-baseline-test-smoke

product-baseline-preflight: ## Check read-only Product baseline prerequisites for one explicit environment
	@./scripts/product-baseline/preflight.sh

product-baseline-capture: ## Capture a Product baseline part with BASELINE_ENV=<environment> BASELINE_PART=<part>
	@./scripts/product-baseline/capture-inventory.sh

product-baseline-test: ## Run a named disposable Product baseline suite with BASELINE_SUITE=<suite>
	@./scripts/product-baseline/validate-suite.sh
ifeq ($(BASELINE_SUITE),smoke)
	+@$(MAKE) --no-print-directory _product-baseline-test-smoke
else
	@./scripts/product-baseline/not-implemented.sh test-suite BASELINE_SUITE
endif

product-baseline-fixtures-check: ## Check Product fixtures, optionally filtered with BASELINE_ENV and BASELINE_PART
	@./scripts/product-baseline/check-inventory.sh

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
