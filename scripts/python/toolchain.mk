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
PYTHON_SCOPE_FILES = $(shell git -C "$(PYTHON_REPOSITORY_ROOT)" ls-files \
	--cached --others --exclude-standard -- '*.py' | LC_ALL=C sort)
PYTHON_FILES ?=
PYTHON_ACCEPTANCE_PATH ?=
PYTHON_ACCEPTANCE_CONFIRM ?=

.PHONY: python-uv-check python-runtime-check python-toolchain-setup python-toolchain-check \
	python-toolchain-info _python-files-check python-scope-list python-format \
	python-format-check python-lint python-examples-format-check python-examples-lint \
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

_python-files-check:
	@test -n "$(strip $(PYTHON_FILES))" || { \
		echo "ERROR: PYTHON_FILES must name the handwritten Python files to format or check." >&2; \
		exit 1; \
	}
	@for path in $(PYTHON_FILES); do \
		case "$$path" in \
			*.py) ;; \
			*) echo "ERROR: selected Python path must end in .py: $$path" >&2; exit 1 ;; \
		esac; \
		test -f "$(PYTHON_REPOSITORY_ROOT)/$$path" || { \
			echo "ERROR: selected Python file does not exist: $$path" >&2; \
			exit 1; \
		}; \
	done

python-format: python-toolchain-check _python-files-check ## Format explicitly selected handwritten Python files
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff format $(PYTHON_FILES)

python-format-check: python-toolchain-check _python-files-check ## Check formatting of explicitly selected Python without mutation
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff format --check $(PYTHON_FILES)

python-lint: python-toolchain-check _python-files-check ## Lint explicitly selected handwritten Python files
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff check $(PYTHON_FILES)

python-examples-format-check: PYTHON_FILES := $(PYTHON_EXAMPLE_FILES)
python-examples-format-check: python-format-check ## Check formatting for executable Python examples

python-examples-lint: PYTHON_FILES := $(PYTHON_EXAMPLE_FILES)
python-examples-lint: python-lint ## Lint executable Python examples

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

_python-acceptance-check:
	@test "$(PYTHON_ACCEPTANCE_CONFIRM)" = "yes" || { \
		echo "ERROR: full acceptance requires PYTHON_ACCEPTANCE_CONFIRM=yes." >&2; \
		exit 1; \
	}
	@test -n "$(strip $(PYTHON_ACCEPTANCE_PATH))" || { \
		echo "ERROR: full acceptance requires an explicit repository-relative PYTHON_ACCEPTANCE_PATH." >&2; \
		exit 1; \
	}
	@test -e "$(PYTHON_REPOSITORY_ROOT)/$(PYTHON_ACCEPTANCE_PATH)" || { \
		echo "ERROR: acceptance selection does not exist: $(PYTHON_ACCEPTANCE_PATH)" >&2; \
		exit 1; \
	}

python-test-acceptance: python-toolchain-check _python-acceptance-check ## Run an explicitly selected full acceptance path
	@cd "$(PYTHON_REPOSITORY_ROOT)" && \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q -m acceptance \
		"$(PYTHON_ACCEPTANCE_PATH)"

python-examples-check: python-examples-format-check python-examples-lint python-test-collect \
	python-examples-test-fast ## Run all non-mutating Python example checks

endif
