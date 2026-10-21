ifndef INSURANCE_HUB_PYTHON_TOOLCHAIN_INCLUDED
INSURANCE_HUB_PYTHON_TOOLCHAIN_INCLUDED := 1

PYTHON_TOOLCHAIN_MK_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
PYTHON_REPOSITORY_ROOT := $(abspath $(PYTHON_TOOLCHAIN_MK_DIR)../..)
PYTHON_VERSION := 3.14.8
PYTEST_VERSION := 9.1.1
RUFF_VERSION := 0.17.1
UV_VERSION := 0.13.1
PYTHON_VENV := $(PYTHON_REPOSITORY_ROOT)/.venv
PYTHON_VENV_PYTHON := $(PYTHON_VENV)/bin/python
PYTHON ?= $(PYTHON_VENV_PYTHON)
UV ?= uv
PYTHON_SELECTED_ENVIRONMENT = $(abspath $(dir $(PYTHON))/..)
PYTHON_EXAMPLE_ROOT := ai/examples/python-testing
PYTHON_EXAMPLE_FILES := \
	$(PYTHON_EXAMPLE_ROOT)/example_cli.py \
	$(PYTHON_EXAMPLE_ROOT)/tests/conftest.py \
	$(PYTHON_EXAMPLE_ROOT)/tests/test_example_cli.py \
	$(PYTHON_EXAMPLE_ROOT)/integration/test_disposable_cli.py
PYTHON_SELECTION_ROOT := scripts/python
PYTHON_SELECTION_FILES := \
	$(PYTHON_SELECTION_ROOT)/validate_selection.py \
	$(PYTHON_SELECTION_ROOT)/test_validate_selection.py
PYTHON_SHARED_TOOLING_FILES := \
	ai/skills/code-review/scripts/check_upstream_sync.py \
	ai/skills/code-review/scripts/test_check_upstream_sync.py \
	scripts/markdown_pre_commit.py \
	scripts/markdown_toc.py \
	scripts/test_markdown_pre_commit.py \
	scripts/python/process_runner.py \
	scripts/python/test_runtime_check.py \
	scripts/python/test_process_runner.py \
	scripts/go/report_card.py \
	scripts/go/select_pr_module.py \
	scripts/go/test_report_card.py \
	scripts/go/test_select_pr_module.py \
	scripts/go/topology.py \
	scripts/go/test_topology.py \
	scripts/go/module/module_tooling.py \
	scripts/go/module/test_module_tooling.py \
	scripts/go/service/release.py \
	scripts/go/service/resolve.py \
	scripts/go/service/test_dispatch.py \
	scripts/go/service/test_release.py
PYTHON_SCAFFOLD_TOOLING_FILES := \
	templates/go-service/scripts/check_copy.py \
	templates/go-service/scripts/scaffold_process.py \
	templates/go-service/scripts/test_check_copy.py \
	templates/go-service/scripts/test_scaffold_process.py \
	templates/go-service/scripts/test_tooling.py \
	templates/go-service/scripts/tooling_runner.py \
	templates/go-service/scripts/test_tooling_runner.py
PYTHON_PRODUCT_TOOLING_FILES := \
	scripts/postgres/service/dispatch.py \
	scripts/postgres/service/test_reader.py \
	scripts/product_baseline/__init__.py \
	scripts/product_baseline/accept.py \
	scripts/product_baseline/catalog_roundtrip.py \
	scripts/product_baseline/capture.py \
	scripts/product_baseline/capture_inventory.py \
	scripts/product_baseline/compare_http.py \
	scripts/product_baseline/fixtures.py \
	scripts/product_baseline/preflight.py \
	scripts/product_baseline/replay.py \
	scripts/product_baseline/test_catalog_roundtrip.py \
	scripts/product_baseline/test_compare_http.py \
	scripts/product_baseline/test_fixtures.py \
	scripts/product_baseline/verify.py \
	scripts/product_deployment/__init__.py \
	scripts/product_deployment/reader.py \
	scripts/product_security/__init__.py \
	scripts/product_security/check_cases.py \
	scripts/product_security/check_java_results.py \
	scripts/product_security/security_test.py \
	scripts/product_security/test_validators.py \
	scripts/product_security/verify.py \
	services/product-service/scripts/check_access_results.py \
	services/product-service/scripts/build_image.py \
	services/product-service/scripts/bootstrap_docs.py \
	services/product-service/scripts/test_check_access_results.py \
	services/product-service/scripts/check_ci.py \
	services/product-service/scripts/check_docs.py \
	services/product-service/scripts/check_render.py \
	services/product-service/scripts/deployment.py \
	services/product-service/scripts/deployment_check.py \
	services/product-service/scripts/deployment_lifecycle.py \
	services/product-service/scripts/integration_test.py \
	services/product-service/scripts/image_check.py \
	services/product-service/scripts/product_process.py \
	services/product-service/scripts/telemetry_continuity_test.py \
	services/product-service/scripts/test_deployment_checks.py \
	services/product-service/scripts/test_deployment_lifecycle.py \
	services/product-service/scripts/test_deployment_operations.py \
	services/product-service/scripts/test_product_tooling.py \
	services/product-service/scripts/test_product_tooling_integration.py
PYTHON_K8S_ADMIN_FILES := \
	k8s/scripts/admin/__init__.py \
	k8s/scripts/admin/cluster.py \
	k8s/scripts/admin/dispatch.py \
	k8s/scripts/admin/qa.py \
	k8s/scripts/admin/runtime.py \
	k8s/scripts/admin/test_cluster.py \
	k8s/scripts/admin/test_qa.py
PYTHON_SCOPE_FILES = $(shell git -C "$(PYTHON_REPOSITORY_ROOT)" ls-files \
	--cached --others --exclude-standard -- '*.py' | LC_ALL=C sort)
PYTHON_FILES ?=
PYTHON_ACCEPTANCE_PATH ?=
PYTHON_ACCEPTANCE_CONFIRM ?=
export PYTHON_FILES PYTHON_ACCEPTANCE_PATH PYTHON_ACCEPTANCE_CONFIRM

.PHONY: python-uv-check python-runtime-check python-toolchain-setup python-toolchain-check \
	python-toolchain-info _python-files-check python-scope-list python-format \
	python-format-check python-lint python-examples-format-check python-examples-lint \
	python-selection-format-check python-selection-lint python-selection-test python-selection-check \
	python-shared-tooling-format-check python-shared-tooling-lint \
	python-shared-tooling-test-fast python-shared-tooling-test-integration \
	python-shared-tooling-check \
	python-scaffold-tooling-format-check python-scaffold-tooling-lint \
	python-scaffold-tooling-test-fast python-scaffold-tooling-check \
	python-product-tooling-format-check python-product-tooling-lint \
	python-product-tooling-test-fast python-product-tooling-test-integration \
	python-product-tooling-check python-k8s-admin-format-check python-k8s-admin-lint \
	python-k8s-admin-test-fast python-k8s-admin-check \
	python-repository-format-check python-repository-lint python-test-collect \
	python-examples-test-fast python-examples-test-integration _python-acceptance-check \
	python-test-acceptance python-examples-check

python-uv-check: ## Verify the pinned uv executable without installing anything
	@command -v "$(UV)" >/dev/null 2>&1 || { \
		echo "ERROR: uv $(UV_VERSION) is required; follow CONTRIBUTING.md#python-toolchain-setup or set UV=/path/to/uv." >&2; \
		exit 1; \
	}
	@actual_version=$$("$(UV)" --version | awk '{print $$2}'); \
	if [ "$$actual_version" != "$(UV_VERSION)" ]; then \
		echo "ERROR: uv $(UV_VERSION) is required, but $(UV) reports $$actual_version; install the pinned version or set UV=/path/to/uv." >&2; \
		exit 1; \
	fi

python-runtime-check: ## Verify the selected CPython 3.14.8 runtime without development dependencies
	@test -x "$(PYTHON)" || { \
		echo "ERROR: selected Python is missing or not executable: $(PYTHON)" >&2; \
		echo "Run 'make python-toolchain-setup' or set PYTHON to a verified CPython $(PYTHON_VERSION) executable." >&2; \
		exit 1; \
	}
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -c 'import platform, sys; expected = ("CPython", "$(PYTHON_VERSION)"); actual = (platform.python_implementation(), platform.python_version()); raise SystemExit(0 if actual == expected else "ERROR: selected Python must be %s %s, got %s %s at %s" % (*expected, *actual, sys.executable))' || { \
		echo "Run 'make python-toolchain-setup' or set PYTHON to a verified CPython $(PYTHON_VERSION) executable." >&2; \
		exit 1; \
	}

python-toolchain-setup: python-uv-check ## Install managed CPython and sync the pinned project environment
	@if [ -e "$(PYTHON_VENV)" ]; then \
		if [ ! -x "$(PYTHON_VENV_PYTHON)" ]; then \
			echo "ERROR: existing $(PYTHON_VENV) has no executable bin/python." >&2; \
			echo "Inspect and preserve its dependency declarations, then move or remove it deliberately before rerunning setup." >&2; \
			exit 1; \
		fi; \
		actual_identity=$$(PYTHONDONTWRITEBYTECODE=1 "$(PYTHON_VENV_PYTHON)" -c 'import platform; print(f"{platform.python_implementation()} {platform.python_version()}")' 2>/dev/null) || { \
			echo "ERROR: unable to inspect existing environment $(PYTHON_VENV)." >&2; \
			echo "Preserve its dependency declarations, then move or remove it deliberately before rerunning setup." >&2; \
			exit 1; \
		}; \
		if [ "$$actual_identity" != "CPython $(PYTHON_VERSION)" ]; then \
			echo "ERROR: existing $(PYTHON_VENV) uses $$actual_identity; CPython $(PYTHON_VERSION) is required." >&2; \
			echo "Record its dependencies, then move or remove the environment deliberately; setup will not replace it automatically." >&2; \
			exit 1; \
		fi; \
	fi
	@"$(UV)" python install --managed-python "$(PYTHON_VERSION)"
	@if [ ! -e "$(PYTHON_VENV)" ]; then \
		"$(UV)" venv --python "$(PYTHON_VERSION)" --managed-python "$(PYTHON_VENV)"; \
	fi
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		UV_PROJECT_ENVIRONMENT="$(PYTHON_VENV)" \
		"$(UV)" sync --frozen --python "$(PYTHON_VENV_PYTHON)"
	@$(MAKE) python-toolchain-check PYTHON="$(PYTHON_VENV_PYTHON)" UV="$(UV)"

python-toolchain-check: python-uv-check python-runtime-check ## Verify pinned Python, pytest, Ruff, and the locked environment
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		UV_PROJECT_ENVIRONMENT="$(PYTHON_SELECTED_ENVIRONMENT)" \
		"$(UV)" sync --frozen --check --python "$(PYTHON)"
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -c 'import importlib.metadata as metadata; expected = {"pytest": "$(PYTEST_VERSION)", "ruff": "$(RUFF_VERSION)"}; actual = {name: metadata.version(name) for name in expected}; raise SystemExit(0 if actual == expected else f"ERROR: expected development tools {expected}, got {actual}")'

python-toolchain-info: python-toolchain-check ## Print selected interpreter, managed build, and tool identities
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -c 'import importlib.metadata as metadata, platform, sys; print(f"implementation={platform.python_implementation()}"); print(f"version={platform.python_version()}"); print(f"executable={sys.executable}"); print(f"build={platform.python_build()[0]} {platform.python_build()[1]}"); print("pytest=%s" % metadata.version("pytest")); print("ruff=%s" % metadata.version("ruff"))'
	@"$(UV)" --version
	@"$(UV)" python list "$(PYTHON_VERSION)" --only-installed

python-scope-list: ## List tracked and new untracked handwritten Python selected for repository checks
	@printf '%s\n' $(PYTHON_SCOPE_FILES)

_python-files-check: python-toolchain-check
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" \
		"$(PYTHON_REPOSITORY_ROOT)/$(PYTHON_SELECTION_ROOT)/validate_selection.py" \
		--root "$(PYTHON_REPOSITORY_ROOT)" --kind files --selection-variable PYTHON_FILES

python-format: _python-files-check ## Format explicitly selected handwritten Python files
	@cd "$(PYTHON_REPOSITORY_ROOT)" && set -f; \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff format -- $${PYTHON_FILES}

python-format-check: _python-files-check ## Check formatting of explicitly selected Python without mutation
	@cd "$(PYTHON_REPOSITORY_ROOT)" && set -f; \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff format --check -- $${PYTHON_FILES}

python-lint: _python-files-check ## Lint explicitly selected handwritten Python files
	@cd "$(PYTHON_REPOSITORY_ROOT)" && set -f; \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff check -- $${PYTHON_FILES}

python-examples-format-check: PYTHON_FILES := $(PYTHON_EXAMPLE_FILES)
python-examples-format-check: python-format-check ## Check formatting for executable Python examples

python-examples-lint: PYTHON_FILES := $(PYTHON_EXAMPLE_FILES)
python-examples-lint: python-lint ## Lint executable Python examples

python-selection-format-check: PYTHON_FILES := $(PYTHON_SELECTION_FILES)
python-selection-format-check: python-format-check ## Check Python selection tooling formatting

python-selection-lint: PYTHON_FILES := $(PYTHON_SELECTION_FILES)
python-selection-lint: python-lint ## Lint Python selection tooling

python-selection-test: python-toolchain-check ## Test repository-relative Python selection guards
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		PYTHONDONTWRITEBYTECODE=1 PYTHONPATH="$(PYTHON_REPOSITORY_ROOT)/$(PYTHON_SELECTION_ROOT)" \
		"$(PYTHON)" -m pytest -q "$(PYTHON_SELECTION_ROOT)/test_validate_selection.py"

python-selection-check: python-selection-format-check python-selection-lint python-selection-test ## Run all non-mutating Python selection-tool checks

python-shared-tooling-format-check: PYTHON_FILES := $(PYTHON_SHARED_TOOLING_FILES)
python-shared-tooling-format-check: python-format-check ## Check shared Python tooling formatting

python-shared-tooling-lint: PYTHON_FILES := $(PYTHON_SHARED_TOOLING_FILES)
python-shared-tooling-lint: python-lint ## Lint shared Python tooling

python-shared-tooling-test-fast: python-toolchain-check ## Run fast shared repository tooling tests
	@cd "$(PYTHON_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q \
		-m 'not integration and not acceptance' \
		ai/skills/code-review/scripts/test_check_upstream_sync.py \
		scripts/test_markdown_pre_commit.py \
		scripts/python/test_runtime_check.py \
		scripts/python/test_process_runner.py \
		scripts/go/test_report_card.py scripts/go/test_select_pr_module.py \
		scripts/go/test_topology.py scripts/go/module/test_module_tooling.py \
		scripts/go/service/test_dispatch.py scripts/go/service/test_release.py

python-shared-tooling-test-integration: python-toolchain-check ## Run disposable shared tooling integration tests
	@cd "$(PYTHON_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q \
		-m integration scripts/go/test_report_card.py scripts/go/module/test_module_tooling.py

python-shared-tooling-check: python-shared-tooling-format-check python-shared-tooling-lint \
	python-shared-tooling-test-fast ## Run non-mutating shared Python tooling checks

python-scaffold-tooling-format-check: PYTHON_FILES := $(PYTHON_SCAFFOLD_TOOLING_FILES)
python-scaffold-tooling-format-check: python-format-check ## Check Go scaffold Python formatting

python-scaffold-tooling-lint: PYTHON_FILES := $(PYTHON_SCAFFOLD_TOOLING_FILES)
python-scaffold-tooling-lint: python-lint ## Lint Go scaffold Python tooling

python-scaffold-tooling-test-fast: python-toolchain-check ## Run fast Go scaffold Python tests
	@cd "$(PYTHON_REPOSITORY_ROOT)/templates/go-service" && \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q -p no:cacheprovider \
		-m 'not integration and not acceptance' scripts/test_check_copy.py \
		scripts/test_scaffold_process.py scripts/test_tooling_runner.py

python-scaffold-tooling-check: python-scaffold-tooling-format-check \
	python-scaffold-tooling-lint python-scaffold-tooling-test-fast ## Check Go scaffold Python tooling

python-product-tooling-format-check: PYTHON_FILES := $(PYTHON_PRODUCT_TOOLING_FILES)
python-product-tooling-format-check: python-format-check ## Check Product Python tooling formatting

python-product-tooling-lint: PYTHON_FILES := $(PYTHON_PRODUCT_TOOLING_FILES)
python-product-tooling-lint: python-lint ## Lint Product Python tooling

python-product-tooling-test-fast: python-toolchain-check ## Run fast Product tooling tests
	@cd "$(PYTHON_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q \
		-m 'not integration and not acceptance' \
		scripts/product_baseline/test_catalog_roundtrip.py \
		scripts/product_baseline/test_compare_http.py \
		scripts/product_baseline/test_fixtures.py \
		scripts/product_security/test_validators.py \
		scripts/postgres/service/test_reader.py \
		services/product-service/scripts/test_check_access_results.py \
		services/product-service/scripts/test_deployment_checks.py \
		services/product-service/scripts/test_deployment_operations.py \
		services/product-service/scripts/test_product_tooling.py

python-product-tooling-test-integration: python-toolchain-check ## Run disposable Product tooling integration tests
	@cd "$(PYTHON_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q \
		-m integration \
		services/product-service/scripts/test_deployment_checks.py \
		services/product-service/scripts/test_product_tooling_integration.py

python-product-tooling-check: python-product-tooling-format-check python-product-tooling-lint \
	python-product-tooling-test-fast python-product-tooling-test-integration ## Run all Product tooling checks

python-k8s-admin-format-check: PYTHON_FILES := $(PYTHON_K8S_ADMIN_FILES)
python-k8s-admin-format-check: python-format-check ## Check Kubernetes administration Python formatting

python-k8s-admin-lint: PYTHON_FILES := $(PYTHON_K8S_ADMIN_FILES)
python-k8s-admin-lint: python-lint ## Lint Kubernetes administration Python tooling

python-k8s-admin-test-fast: python-toolchain-check ## Run fake-boundary Kubernetes administration tests
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		PYTHONDONTWRITEBYTECODE=1 PYTHONPATH="$(PYTHON_REPOSITORY_ROOT)/k8s/scripts/admin" \
		"$(PYTHON)" -m pytest -q -p no:cacheprovider -m 'not integration and not acceptance' \
		k8s/scripts/admin/test_cluster.py k8s/scripts/admin/test_qa.py

python-k8s-admin-check: python-k8s-admin-format-check python-k8s-admin-lint \
	python-k8s-admin-test-fast ## Run non-mutating Kubernetes administration checks

python-repository-format-check: PYTHON_FILES = $(PYTHON_SCOPE_FILES)
python-repository-format-check: python-format-check ## Check all visible handwritten Python without mutation

python-repository-lint: PYTHON_FILES = $(PYTHON_SCOPE_FILES)
python-repository-lint: python-lint ## Lint all visible handwritten Python without mutation

python-test-collect: python-toolchain-check ## Collect default fast pytest scope without running tests
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest --collect-only -q

python-examples-test-fast: python-toolchain-check ## Run fast executable Python example tests
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q \
		-m 'not integration and not acceptance' "$(PYTHON_EXAMPLE_ROOT)/tests"

python-examples-test-integration: python-toolchain-check ## Run disposable Python example integration tests explicitly
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		PYTHONDONTWRITEBYTECODE=1 PYTHON_EXAMPLE_INTEGRATION=1 \
		"$(PYTHON)" -m pytest -q -m integration "$(PYTHON_EXAMPLE_ROOT)/integration"

_python-acceptance-check: python-toolchain-check
	@test "$${PYTHON_ACCEPTANCE_CONFIRM}" = "yes" || { \
		echo "ERROR: full acceptance requires PYTHON_ACCEPTANCE_CONFIRM=yes." >&2; \
		exit 1; \
	}
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" \
		"$(PYTHON_REPOSITORY_ROOT)/$(PYTHON_SELECTION_ROOT)/validate_selection.py" \
		--root "$(PYTHON_REPOSITORY_ROOT)" --kind acceptance \
		--selection-variable PYTHON_ACCEPTANCE_PATH

python-test-acceptance: _python-acceptance-check ## Run an explicitly selected full acceptance path
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q -m acceptance \
		"$${PYTHON_ACCEPTANCE_PATH}"

python-examples-check: python-examples-format-check python-examples-lint python-test-collect \
	python-examples-test-fast ## Run all non-mutating Python example checks

endif
