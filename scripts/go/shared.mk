# Root-owned orchestration for validated, automatic library publication.
GO_SHARED_TOOL := $(ROOT_DIR)scripts/go/shared_cli.py
GO_SHARED_ACTIONLINT ?= $(ROOT_DIR)services/product-service/.tools/bin/actionlint
export PYTHON

.PHONY: go-shared-plan go-shared-validate go-shared-results-check go-shared-phase \
	go-shared-test go-shared-test-integration go-library-release-validate \
	go-library-release-tag go-library-release-verify go-library-release-publish go-shared-check-ci

go-shared-check-ci: python-runtime-check ## Lint additive shared workflows using the existing pinned CI tool
	@"$(GO_SHARED_ACTIONLINT)" -shellcheck= -pyflakes= \
		"$(ROOT_DIR).github/workflows/go-pr-shared.yml" "$(ROOT_DIR).github/workflows/go-release-lib.yml"

go-shared-plan: python-runtime-check ## Plan complete PR obligations (PR_BASE/PR_HEAD full SHAs)
	@"$(PYTHON)" "$(GO_SHARED_TOOL)" plan

go-shared-validate: python-runtime-check ## Validate MODULE_DIR against recomputed PR obligations
	@"$(PYTHON)" "$(GO_SHARED_TOOL)" validate

go-shared-results-check: python-runtime-check ## Reconcile every required module/phase/mode result
	@"$(PYTHON)" "$(GO_SHARED_TOOL)" results

go-shared-phase: python-runtime-check ## Run SERVICEKIT_PHASE through a live candidate controller
	@"$(PYTHON)" "$(GO_SHARED_TOOL)" phase

go-shared-test: python-toolchain-check ## Test shared registration, selection, guards and release contracts
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q -m 'not integration' scripts/go/test_shared_*.py

go-shared-test-integration: python-toolchain-check ## Exercise disposable candidate and process boundaries
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q -m integration scripts/go/test_shared_*.py

go-library-release-validate: python-runtime-check ## Read-only exact-source validation for a main push
	@"$(PYTHON)" "$(GO_SHARED_TOOL)" release-validate

go-library-release-tag: python-runtime-check ## Publish the prefixed tag at the validated main push SHA
	@"$(PYTHON)" "$(GO_SHARED_TOOL)" release-tag

go-library-release-verify: python-runtime-check ## Verify public checksums and fresh standalone/container resolution
	@"$(PYTHON)" "$(GO_SHARED_TOOL)" release-verify

go-library-release-publish: python-runtime-check ## Publish and read back an attested qualification asset
	@"$(PYTHON)" "$(GO_SHARED_TOOL)" release-publish
