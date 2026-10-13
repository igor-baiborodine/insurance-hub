#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf 'product-deploy-render: %s\n' "$*" >&2
	exit 1
}

command -v "${KUBECTL:-kubectl}" >/dev/null 2>&1 || fail "kubectl is required"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly script_dir
repo_root="$(git -C "${script_dir}" rev-parse --show-toplevel 2>/dev/null)" || \
	fail "repository root could not be resolved"
readonly repo_root

render_environment() {
	local environment="$1"
	local overlay
	case "${environment}" in
		local-dev) overlay="k8s/overlays/local-dev/svc/product" ;;
		qa) overlay="k8s/overlays/qa/svc/product" ;;
		*) fail "DEPLOY_ENV must be local-dev or qa" ;;
	esac
	[[ -f "${repo_root}/${overlay}/kustomization.yaml" ]] || fail "missing overlay: ${overlay}"
	printf '# Product Go overlay: %s\n' "${environment}"
	"${KUBECTL:-kubectl}" kustomize "${repo_root}/${overlay}"
}

case "${DEPLOY_ENV:-}" in
	"")
		render_environment local-dev
		printf '%s\n' '---'
		render_environment qa
		;;
	local-dev | qa) render_environment "${DEPLOY_ENV}" ;;
	*) fail "DEPLOY_ENV must be empty, local-dev, or qa" ;;
esac
