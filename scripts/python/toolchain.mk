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

.PHONY: python-uv-check python-runtime-check python-toolchain-setup python-toolchain-check \
	python-toolchain-info

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

endif
