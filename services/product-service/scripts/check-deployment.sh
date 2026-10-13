#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf 'product-deploy-check: %s\n' "$*" >&2
	exit 1
}

for command_name in "${KUBECTL:-kubectl}" "${JQ:-jq}" "${PYTHON:-python3}" cp find; do
	command -v "${command_name}" >/dev/null 2>&1 || fail "required command is unavailable: ${command_name}"
done

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly script_dir
repo_root="$(git -C "${script_dir}" rev-parse --show-toplevel 2>/dev/null)" || \
	fail "repository root could not be resolved"
readonly repo_root
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/product-deploy-check.XXXXXX")"
readonly temporary_root

cleanup() {
	case "${temporary_root}" in
		"${TMPDIR:-/tmp}"/product-deploy-check.*) find -P "${temporary_root}" -depth -delete ;;
		*) printf 'product-deploy-check: refusing unexpected temporary path: %s\n' "${temporary_root}" >&2 ;;
	esac
}
trap cleanup EXIT HUP INT TERM

render() {
	local source="$1"
	local output="$2"
	"${KUBECTL:-kubectl}" kustomize "${source}" >"${output}"
}

to_json() {
	local source="$1"
	local output="$2"
	# kubectl client dry-run still uses API discovery. Parse YAML locally instead.
	"${script_dir}/../.tools/ci-venv/bin/python" -c \
		'import json, sys, yaml; json.dump(list(yaml.safe_load_all(sys.stdin)), sys.stdout)' \
		<"${source}" >"${output}"
}

[[ -x "${script_dir}/../.tools/ci-venv/bin/python" ]] || \
	fail "pinned YAML parser is required; run make -C services/product-service bootstrap-ci-tools"

render "${repo_root}/k8s/overlays/local-dev/svc/product" "${temporary_root}/local-go.yaml"
render "${repo_root}/k8s/overlays/local-dev/svc/product/legacy" "${temporary_root}/local-legacy.yaml"
render "${repo_root}/k8s/overlays/qa/svc/product" "${temporary_root}/qa-go.yaml"
render "${repo_root}/k8s/overlays/qa/svc/product/legacy" "${temporary_root}/qa-legacy.yaml"
render "${repo_root}/k8s/overlays/qa/svc" "${temporary_root}/qa-aggregate.yaml"

cp -a "${repo_root}/k8s" "${temporary_root}/k8s"
cat >>"${temporary_root}/k8s/overlays/qa/svc/kustomization.yaml" <<'EOF'

  - ./product
EOF
render "${temporary_root}/k8s/overlays/qa/svc" "${temporary_root}/qa-preview.yaml"

for name in local-go local-legacy qa-go qa-legacy qa-aggregate qa-preview; do
	to_json "${temporary_root}/${name}.yaml" "${temporary_root}/${name}.json"
done

checker_arguments=(
	--local-go "${temporary_root}/local-go.json"
	--local-legacy "${temporary_root}/local-legacy.json"
	--qa-go "${temporary_root}/qa-go.json"
	--qa-legacy "${temporary_root}/qa-legacy.json"
	--qa-aggregate "${temporary_root}/qa-aggregate.json"
	--qa-preview "${temporary_root}/qa-preview.json"
)
"${PYTHON:-python3}" "${script_dir}/check-render.py" "${checker_arguments[@]}"
"${PYTHON:-python3}" "${script_dir}/test-deployment-checks.py" \
	--checker "${script_dir}/check-render.py" "${checker_arguments[@]}"
