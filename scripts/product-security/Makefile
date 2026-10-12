.PHONY: product-security-test product-security-check product-security-test-tooling

product-security-test: ## Run the isolated Java gateway to Go Product security smoke check
	@./scripts/product-security/test.sh

product-security-check: ## Run access fixtures, Java baseline, and combined security checks
	+@$(MAKE) --no-print-directory product-baseline-fixtures-check BASELINE_PART=access
	+@$(MAKE) --no-print-directory product-baseline-test BASELINE_SUITE=access
	+@$(MAKE) --no-print-directory product-security-test

product-security-test-tooling: ## Prove controlled security gate failures in disposable fixtures
	@./scripts/product-security/test-tooling.sh
