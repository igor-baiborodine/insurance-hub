# Consumer-owned framework interface. Included once by the root Makefile after Python tooling.
AI_FRAMEWORK_ROOT := $(ROOT_DIR)ai/framework
AI_FRAMEWORK_LANGUAGE ?= go
AI_FRAMEWORK_PROFILE ?= ai/profile.json
AI_FRAMEWORK_PYTHON_FILES := ai/checks/check_ai_framework.py ai/checks/test_check_ai_framework.py
AI_CHECK_PYTHON_FILES := $(AI_FRAMEWORK_PYTHON_FILES)
AI_CHECK_TICKET_DIR ?=
export AI_CHECK_TICKET_DIR

.PHONY: ai-check ai-check-format-check ai-check-lint ai-check-test-fast ai-check-validate \
	ai-framework-integrity ai-framework-compose ai-framework-freshness ai-framework-source

ai-check-format-check: PYTHON_FILES := $(AI_CHECK_PYTHON_FILES)
ai-check-format-check: python-format-check ## Check consumer AI validator formatting

ai-check-lint: PYTHON_FILES := $(AI_CHECK_PYTHON_FILES)
ai-check-lint: python-lint ## Lint consumer AI validator and fixtures

ai-check-test-fast: python-toolchain-check ## Test consumer compatibility and ticket validation
	@cd "$(ROOT_DIR)" && "$(PYTHON)" -B -m pytest -q ai/checks/test_check_ai_framework.py

ai-check-validate: python-runtime-check ## Validate consumer links, routing and optional local ticket
	@cd "$(ROOT_DIR)" && "$(PYTHON)" -B ai/checks/check_ai_framework.py

ai-framework-integrity: python-runtime-check ## Check initialized framework against the index pin offline
	@cd "$(ROOT_DIR)" && "$(PYTHON)" -B "$(AI_FRAMEWORK_ROOT)/tools/framework_check.py" integrity \
		--consumer-root "$(ROOT_DIR)" --framework-root "$(AI_FRAMEWORK_ROOT)" --profile "$(AI_FRAMEWORK_PROFILE)"

ai-framework-compose: python-runtime-check ## Validate selected language and both installed clients offline
	@cd "$(ROOT_DIR)" && "$(PYTHON)" -B "$(AI_FRAMEWORK_ROOT)/tools/framework_check.py" compose \
		--consumer-root "$(ROOT_DIR)" --framework-root "$(AI_FRAMEWORK_ROOT)" --profile "$(AI_FRAMEWORK_PROFILE)" \
		--language "$(AI_FRAMEWORK_LANGUAGE)" --client codex --client claude \
		--require markdown-format --require markdown-check --require snapshot --require artifact-preflight

ai-framework-freshness: python-runtime-check ## Query upstream main and require exact pin equality (network)
	@cd "$(ROOT_DIR)" && "$(PYTHON)" -B "$(AI_FRAMEWORK_ROOT)/tools/framework_check.py" freshness \
		--consumer-root "$(ROOT_DIR)" --framework-root "$(AI_FRAMEWORK_ROOT)" --profile "$(AI_FRAMEWORK_PROFILE)" --timeout 20

ai-framework-source: python-toolchain-check ## Validate pinned shared source with declared PyYAML
	@cd "$(ROOT_DIR)" && "$(PYTHON)" -B "$(AI_FRAMEWORK_ROOT)/tools/framework_check.py" source \
		--framework-root "$(AI_FRAMEWORK_ROOT)"

ai-check: ai-check-format-check ai-check-lint ai-check-test-fast ai-check-validate \
	ai-framework-source ai-framework-integrity ai-framework-compose ## Check shared source and consumer composition; no network freshness claim
