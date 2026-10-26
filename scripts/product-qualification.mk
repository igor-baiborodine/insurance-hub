################################################################################
# Read-only Product QA qualification targets (repository root)
################################################################################

PRODUCT_QUALIFICATION_CONTEXT ?=
PRODUCT_QUALIFICATION_NAMESPACE ?=
PRODUCT_QUALIFICATION_PHASE ?=
PRODUCT_QUALIFICATION_REVISION ?=
PRODUCT_QUALIFICATION_IMAGE ?=
PRODUCT_QUALIFICATION_PREVIEW_URL ?=
PRODUCT_QUALIFICATION_TOKEN_FILE ?=
PRODUCT_QUALIFICATION_OUTPUT ?=
PRODUCT_QUALIFICATION_COMPARE_TO ?=
PRODUCT_QUALIFICATION_GRPC_ADDRESS ?=
PRODUCT_QUALIFICATION_GRPC_CLIENT ?=
PRODUCT_QUALIFICATION_PROMETHEUS_URL ?=
PRODUCT_QUALIFICATION_ESTABLISHED_URL ?=
PRODUCT_QUALIFICATION_GATEWAY_POD ?=
PRODUCT_QUALIFICATION_PRODUCT_POD ?=
PRODUCT_QUALIFICATION_DATABASE_POD ?=
PRODUCT_QUALIFICATION_EXPECTED_REVISION ?=
PRODUCT_QUALIFICATION_FORWARD_SERVICE ?=
PRODUCT_QUALIFICATION_LOCAL_PORT ?=
PRODUCT_QUALIFICATION_PUBLISHED_AT ?=
PRODUCT_QUALIFICATION_DECIDED_AT ?=
PRODUCT_QUALIFICATION_SIGNALS_FILE ?=
PRODUCT_QUALIFICATION_ATTRIBUTION_FILE ?=

.PHONY: product-qualification-render product-qualification-status product-qualification-forward \
	product-qualification-core product-qualification-grpc product-qualification-measure \
	product-qualification-restore product-qualification-test \
	product-qualification-established-status product-qualification-established-core \
	product-qualification-established-measure product-qualification-established-observe \
	product-qualification-established-restore

product-qualification-render: python-runtime-check ## Check isolated QA preview render without applying it
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.product_qualification.ops render

product-qualification-status: python-runtime-check ## Inspect QA Flux/preview route at PRODUCT_QUALIFICATION_EXPECTED_REVISION and PHASE
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.product_qualification.ops status \
		--expected-revision "$(PRODUCT_QUALIFICATION_EXPECTED_REVISION)" \
		--phase "$(PRODUCT_QUALIFICATION_PHASE)"

product-qualification-forward: python-runtime-check ## Foreground localhost forward; set FORWARD_SERVICE and LOCAL_PORT
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.product_qualification.ops forward \
		--service "$(PRODUCT_QUALIFICATION_FORWARD_SERVICE)" \
		--local-port "$(PRODUCT_QUALIFICATION_LOCAL_PORT)"

product-qualification-restore: python-runtime-check ## Verify Java route restoration from remote publication time (PRODUCT_QUALIFICATION_* variables)
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.product_qualification.ops restore \
		--expected-revision "$(PRODUCT_QUALIFICATION_EXPECTED_REVISION)" \
		--published-at "$(PRODUCT_QUALIFICATION_PUBLISHED_AT)" \
		--preview-url "$(PRODUCT_QUALIFICATION_PREVIEW_URL)" \
		--established-url "$(PRODUCT_QUALIFICATION_ESTABLISHED_URL)" \
		--token-file "$(PRODUCT_QUALIFICATION_TOKEN_FILE)" \
		--output "$(PRODUCT_QUALIFICATION_OUTPUT)"

product-qualification-core: python-runtime-check ## Check authenticated preview reads against QA fixture (PRODUCT_QUALIFICATION_* variables)
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.product_qualification.core \
		--context "$(PRODUCT_QUALIFICATION_CONTEXT)" \
		--namespace "$(PRODUCT_QUALIFICATION_NAMESPACE)" \
		--phase "$(PRODUCT_QUALIFICATION_PHASE)" \
		--revision "$(PRODUCT_QUALIFICATION_REVISION)" \
		--image "$(PRODUCT_QUALIFICATION_IMAGE)" \
		--preview-url "$(PRODUCT_QUALIFICATION_PREVIEW_URL)" \
		--token-file "$(PRODUCT_QUALIFICATION_TOKEN_FILE)" \
		--output "$(PRODUCT_QUALIFICATION_OUTPUT)" $(if $(strip $(PRODUCT_QUALIFICATION_COMPARE_TO)),--compare-to "$(PRODUCT_QUALIFICATION_COMPARE_TO)",)

product-qualification-grpc: ## Check direct Go gRPC reads via built client (PRODUCT_QUALIFICATION_* variables)
	@test -n "$(strip $(PRODUCT_QUALIFICATION_GRPC_CLIENT))" || { echo "PRODUCT_QUALIFICATION_GRPC_CLIENT is required" >&2; exit 1; }
	@"$(PRODUCT_QUALIFICATION_GRPC_CLIENT)" \
		--context "$(PRODUCT_QUALIFICATION_CONTEXT)" \
		--namespace "$(PRODUCT_QUALIFICATION_NAMESPACE)" \
		--address "$(PRODUCT_QUALIFICATION_GRPC_ADDRESS)" \
		--fixture "legacy/product-service/src/test/resources/product-read-baseline/http/qa.json"

product-qualification-measure: python-runtime-check ## Run fixed 60s+300s preview load with QA controls (PRODUCT_QUALIFICATION_* variables)
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.product_qualification.measure \
		--context "$(PRODUCT_QUALIFICATION_CONTEXT)" \
		--namespace "$(PRODUCT_QUALIFICATION_NAMESPACE)" \
		--phase "$(PRODUCT_QUALIFICATION_PHASE)" \
		--revision "$(PRODUCT_QUALIFICATION_REVISION)" \
		--image "$(PRODUCT_QUALIFICATION_IMAGE)" \
		--preview-url "$(PRODUCT_QUALIFICATION_PREVIEW_URL)" \
		--token-file "$(PRODUCT_QUALIFICATION_TOKEN_FILE)" \
		--output "$(PRODUCT_QUALIFICATION_OUTPUT)" \
		--prometheus-url "$(PRODUCT_QUALIFICATION_PROMETHEUS_URL)" \
		--established-url "$(PRODUCT_QUALIFICATION_ESTABLISHED_URL)" \
		--gateway-pod "$(PRODUCT_QUALIFICATION_GATEWAY_POD)" \
		--product-pod "$(PRODUCT_QUALIFICATION_PRODUCT_POD)" \
		--database-pod "$(PRODUCT_QUALIFICATION_DATABASE_POD)"

product-qualification-test: python-toolchain-check ## Run focused offline Product qualification checks
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q scripts/product_qualification/test_core.py scripts/product_qualification/test_measure.py scripts/product_qualification/test_ops.py scripts/product_qualification/test_established.py

product-qualification-established-status: python-runtime-check ## Inspect Ready QA established route at exact revision/phase
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.product_qualification.ops status-established \
		--expected-revision "$(PRODUCT_QUALIFICATION_EXPECTED_REVISION)" \
		--phase "$(PRODUCT_QUALIFICATION_PHASE)"

product-qualification-established-core: python-runtime-check ## Verify established QA catalog against accepted fixture
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.product_qualification.core \
		--context "$(PRODUCT_QUALIFICATION_CONTEXT)" \
		--namespace "$(PRODUCT_QUALIFICATION_NAMESPACE)" \
		--phase "$(PRODUCT_QUALIFICATION_PHASE)" \
		--revision "$(PRODUCT_QUALIFICATION_REVISION)" \
		--image "$(PRODUCT_QUALIFICATION_IMAGE)" \
		--route established \
		--established-url "$(PRODUCT_QUALIFICATION_ESTABLISHED_URL)" \
		--token-file "$(PRODUCT_QUALIFICATION_TOKEN_FILE)" \
		--output "$(PRODUCT_QUALIFICATION_OUTPUT)" $(if $(strip $(PRODUCT_QUALIFICATION_COMPARE_TO)),--compare-to "$(PRODUCT_QUALIFICATION_COMPARE_TO)",)

product-qualification-established-measure: python-runtime-check ## Run fixed 60s+300s QA established-route measurement
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.product_qualification.measure \
		--context "$(PRODUCT_QUALIFICATION_CONTEXT)" \
		--namespace "$(PRODUCT_QUALIFICATION_NAMESPACE)" \
		--phase "$(PRODUCT_QUALIFICATION_PHASE)" \
		--revision "$(PRODUCT_QUALIFICATION_REVISION)" \
		--image "$(PRODUCT_QUALIFICATION_IMAGE)" \
		--route established \
		--token-file "$(PRODUCT_QUALIFICATION_TOKEN_FILE)" \
		--output "$(PRODUCT_QUALIFICATION_OUTPUT)" \
		--prometheus-url "$(PRODUCT_QUALIFICATION_PROMETHEUS_URL)" \
		--established-url "$(PRODUCT_QUALIFICATION_ESTABLISHED_URL)" \
		--gateway-pod "$(PRODUCT_QUALIFICATION_GATEWAY_POD)" \
		--product-pod "$(PRODUCT_QUALIFICATION_PRODUCT_POD)" \
		--database-pod "$(PRODUCT_QUALIFICATION_DATABASE_POD)"

product-qualification-established-observe: python-runtime-check ## Run fixed 900s QA final-Go observation
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.product_qualification.measure \
		--context "$(PRODUCT_QUALIFICATION_CONTEXT)" \
		--namespace "$(PRODUCT_QUALIFICATION_NAMESPACE)" \
		--phase go \
		--revision "$(PRODUCT_QUALIFICATION_REVISION)" \
		--image "$(PRODUCT_QUALIFICATION_IMAGE)" \
		--route established --observation \
		--signals-file "$(PRODUCT_QUALIFICATION_SIGNALS_FILE)" \
		--token-file "$(PRODUCT_QUALIFICATION_TOKEN_FILE)" \
		--output "$(PRODUCT_QUALIFICATION_OUTPUT)" \
		--prometheus-url "$(PRODUCT_QUALIFICATION_PROMETHEUS_URL)" \
		--established-url "$(PRODUCT_QUALIFICATION_ESTABLISHED_URL)" \
		--gateway-pod "$(PRODUCT_QUALIFICATION_GATEWAY_POD)" \
		--product-pod "$(PRODUCT_QUALIFICATION_PRODUCT_POD)" \
		--database-pod "$(PRODUCT_QUALIFICATION_DATABASE_POD)"

product-qualification-established-restore: python-runtime-check ## Verify QA Java restoration within 900s of publication
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.product_qualification.ops restore-established \
		--expected-revision "$(PRODUCT_QUALIFICATION_EXPECTED_REVISION)" \
		--decision-at "$(PRODUCT_QUALIFICATION_DECIDED_AT)" \
		--published-at "$(PRODUCT_QUALIFICATION_PUBLISHED_AT)" \
		--established-url "$(PRODUCT_QUALIFICATION_ESTABLISHED_URL)" \
		--token-file "$(PRODUCT_QUALIFICATION_TOKEN_FILE)" \
		--attribution-file "$(PRODUCT_QUALIFICATION_ATTRIBUTION_FILE)" \
		--output "$(PRODUCT_QUALIFICATION_OUTPUT)"
