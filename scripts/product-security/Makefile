.PHONY: product-security-test product-security-check product-security-test-tooling \
	product-security-test-tooling-integration product-security-verify

product-security-verify: python-runtime-check ## Read-only Product HTTP/gRPC and management probes against explicit target endpoints
	@./scripts/product-security/verify.sh

product-security-test: python-runtime-check ## Run the isolated Java gateway to Go Product security smoke check
	@./scripts/product-security/test.sh

product-security-check: ## Run access fixtures, Java baseline, and combined security checks
	+@$(MAKE) --no-print-directory product-baseline-fixtures-check BASELINE_PART=access
	+@$(MAKE) --no-print-directory product-baseline-test BASELINE_SUITE=access
	+@$(MAKE) --no-print-directory product-security-test

product-security-test-tooling: python-toolchain-check ## Prove controlled security gate failures in disposable fixtures
	@./scripts/product-security/test-tooling.sh

product-security-test-tooling-integration: python-runtime-check ## Run the real Java/Go/Docker Product security boundary
	@./scripts/product-security/test.sh
