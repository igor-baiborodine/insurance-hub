#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly script_dir
repo_root="$(cd -- "${script_dir}/../.." && pwd -P)"
readonly repo_root
temp_parent="$(cd -- "${TMPDIR:-/tmp}" && pwd -P)"
readonly temp_parent
fixture_root="$(mktemp -d "${temp_parent}/issue-123-go-topology.XXXXXX")"
readonly fixture_root

case "${fixture_root}" in
	"${temp_parent}"/issue-123-go-topology.*) ;;
	*)
		printf 'go-topology-test: refusing unexpected temporary path: %s\n' "${fixture_root}" >&2
		exit 1
		;;
esac

cleanup() {
	case "${fixture_root}" in
		"${temp_parent}"/issue-123-go-topology.*)
			if [[ -d "${fixture_root}" ]]; then
				find -P "${fixture_root}" -depth -delete
			fi
			;;
		*)
			printf 'go-topology-test: refusing to clean unexpected path: %s\n' "${fixture_root}" >&2
			;;
	esac
}

on_signal() {
	local status="$1"
	trap - EXIT HUP INT TERM
	cleanup
	exit "${status}"
}

trap cleanup EXIT
trap 'on_signal 129' HUP
trap 'on_signal 130' INT
trap 'on_signal 143' TERM

fail() {
	printf 'go-topology-test: %s\n' "$*" >&2
	exit 1
}

command -v jq >/dev/null 2>&1 || fail "jq >= 1.6 is required"
command -v go >/dev/null 2>&1 || fail "Go is required for controlled package graphs"
command -v make >/dev/null 2>&1 || fail "Make is required"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required"
export CHECKER_ROOT="${script_dir}"

if [[ "${GO_TOPOLOGY_TEST_PAUSE_AFTER_SETUP:-0}" == "1" ]]; then
	printf 'go-topology-test: pause fixture ready: %s\n' "${fixture_root}"
	while :; do
		sleep 1
	done
fi
if [[ "${GO_TOPOLOGY_TEST_FAIL_AFTER_SETUP:-0}" == "1" ]]; then
	fail "controlled failure after fixture setup"
fi

snapshot_checkout() {
	local output_path="$1"
	local absolute_path relative_path digest
	: >"${output_path}"
	while IFS= read -r -d '' absolute_path; do
		relative_path="${absolute_path#"${repo_root}/"}"
		digest="$(sha256sum "${absolute_path}" | awk '{print $1}')"
		printf '%s\t%s\n' "${relative_path}" "${digest}" >>"${output_path}"
	done < <(
		find -P "${repo_root}" \
			\( -path "${repo_root}/.git" -o -path "${repo_root}/ai/artifacts" \
				-o -name .tools -o -name vendor -o -name node_modules -o -name third_party \) -prune \
			-o -type f \( -name '*.go' -o -name go.mod -o -name go.sum -o -name go.work \
				-o -name go.work.sum -o -name Makefile -o -name 'go-module-topology.json' \
				-o -name '*.mk' -o -path '*/scripts/go/*.sh' \) -print0 | sort -z
	)
}

snapshot_fixture() {
	local case_root="$1"
	local output_path="$2"
	local absolute_path relative_path digest
	: >"${output_path}"
	while IFS= read -r -d '' absolute_path; do
		relative_path="${absolute_path#"${case_root}/"}"
		digest="$(sha256sum "${absolute_path}" | awk '{print $1}')"
		printf '%s\t%s\n' "${relative_path}" "${digest}" >>"${output_path}"
	done < <(find -P "${case_root}" -type f -print0 | sort -z)
}

readonly checkout_before="${fixture_root}/checkout-before"
snapshot_checkout "${checkout_before}"

install_checker() {
	local case_root="$1"
	mkdir -p "${case_root}/.github/workflows"
	: >"${case_root}/.go-topology-test-fixture"
	cat >"${case_root}/Makefile" <<'EOF'
.PHONY: go-topology-check go-modules-check
go-topology-check:
	@GO_TOPOLOGY_TEST_MODE=1 \
		GO_TOPOLOGY_FIXTURE_ROOT="$(CURDIR)" \
		GO_TOPOLOGY_FIXTURE_INVENTORY="$(CURDIR)/go-module-topology.json" \
		"$(CHECKER_ROOT)/check-topology.sh"
go-modules-check: go-topology-check
	@GO_TOPOLOGY_TEST_MODE=1 \
		GO_TOPOLOGY_FIXTURE_ROOT="$(CURDIR)" \
		GO_TOPOLOGY_FIXTURE_INVENTORY="$(CURDIR)/go-module-topology.json" \
		GO_MODULE_CALLER_VARIABLES="" MAKE_COMMAND="$(MAKE)" \
		"$(CHECKER_ROOT)/check-modules.sh"
EOF
}

write_ci_workflow() {
	local case_root="$1"
	local modules_json="$2"
	local workspaces_json="$3"
	local workflow_path="${case_root}/.github/workflows/test.yml"
	local directory role
	{
		printf '%s\n' \
			'name: Fixture topology CI' \
			'on:' \
			'  pull_request:' \
			'    branches:' \
			'      - main' \
			'    paths:'
		while IFS= read -r directory; do
			if [[ "${directory}" != "." ]]; then
				printf "      - '%s/**/*.go'\n" "${directory}"
				printf "      - '%s/go.mod'\n" "${directory}"
				printf "      - '%s/go.sum'\n" "${directory}"
			fi
		done < <(jq -nr --argjson modules "${modules_json}" '$modules[].directory')
		printf '%s\n' \
			'permissions:' \
			'  contents: read' \
			'concurrency:' \
			'  group: fixture-${{ github.event.pull_request.number }}' \
			'  cancel-in-progress: true' \
			'jobs:' \
			'  validate:' \
			'    runs-on: ubuntu-24.04' \
			'    steps:' \
			'      - uses: actions/checkout@v6' \
			'        with:' \
			'          ref: ${{ github.event.pull_request.head.sha }}' \
			'          fetch-depth: 0' \
			'      - name: Select Go module' \
			'        id: select' \
			'        shell: bash' \
			'        run: |' \
			'          set -euo pipefail' \
			'          merge_base=$(git merge-base "$PR_BASE" "$PR_HEAD")' \
			'          declare -A selected=()' \
			"          while IFS= read -r -d '' changed_path; do" \
			'            case "$changed_path" in'
		while IFS= read -r directory; do
			[[ "${directory}" == "." ]] || printf '%s\n' \
				"              ${directory}/*) selected[${directory}]=1 ;;"
		done < <(jq -nr --argjson modules "${modules_json}" '$modules[].directory')
		printf '%s\n' \
			'            esac' \
			'          done < <(git diff --name-only --diff-filter=ACDMRT -z "$merge_base" "$PR_HEAD")' \
			"          mapfile -t modules < <(printf '%s\\n' \"\${!selected[@]}\" | sort)" \
			'          if (( ${#modules[@]} == 0 )); then' \
			"            echo 'module_dir=' >> \"\$GITHUB_OUTPUT\"" \
			'            exit 0' \
			'          fi' \
			'          if (( ${#modules[@]} > 1 )); then' \
			"            echo 'Multiple Go modules changed; split the pull request' >&2" \
			'            exit 1' \
			'          fi' \
			'          module_dir=${modules[0]}' \
			'          case "$module_dir" in'
		while IFS=$'\t' read -r directory role; do
			[[ "${directory}" == "." ]] || printf '%s\n' \
				"            ${directory}) module_role='${role}' ;;"
		done < <(jq -nr --argjson modules "${modules_json}" '$modules[] | [.directory, .role] | @tsv')
		printf '%s\n' \
			'          esac' \
			'          echo "module_dir=$module_dir" >> "$GITHUB_OUTPUT"' \
			'          echo "module_role=$module_role" >> "$GITHUB_OUTPUT"' \
			'          echo "module_key=${module_dir//\//-}" >> "$GITHUB_OUTPUT"' \
			'          echo "go_sum=$module_dir/go.sum" >> "$GITHUB_OUTPUT"' \
			"          echo 'validation_target=check-go-pr' >> \"\$GITHUB_OUTPUT\"" \
			'          echo "format_base=$merge_base" >> "$GITHUB_OUTPUT"' \
			'          echo "coverage_file=$module_dir/coverage.txt" >> "$GITHUB_OUTPUT"' \
			'          echo "coverage_flag=${module_dir##*/}" >> "$GITHUB_OUTPUT"' \
			'      - uses: actions/setup-go@v7' \
			'        with:' \
			'          cache: true' \
			'          cache-dependency-path: ${{ steps.select.outputs.go_sum }}' \
			'      - uses: actions/cache@v5' \
			'        with:' \
			'          path: ${{ steps.select.outputs.module_dir }}/.tools' \
			'          key: ${{ steps.select.outputs.module_key }}-scripts/go/module/common.mk' \
			'      - run: make -C "$MODULE_DIR" bootstrap-go-tools' \
			'      - env:' \
			'          COVERAGE_FILE: ${{ github.workspace }}/${{ steps.select.outputs.coverage_file }}' \
			'          FORMAT_BASE: ${{ steps.select.outputs.format_base }}' \
			'        run: |' \
			"          if [[ \"\$MODULE_ROLE\" != 'service-scaffold' ]]; then" \
			'            rm -f -- "$COVERAGE_FILE"' \
			'          fi' \
			'          make -C "$MODULE_DIR" "$VALIDATION_TARGET" FORMAT_SCOPE=changed FORMAT_BASE="$FORMAT_BASE"' \
			"      - if: success() && steps.select.outputs.module_role != 'service-scaffold'" \
			'        uses: codecov/codecov-action@v7' \
			'        with:' \
			'          files: ${{ steps.select.outputs.coverage_file }}' \
			'          flags: ${{ steps.select.outputs.coverage_flag }}' \
			'          disable_search: true' \
			'          fail_ci_if_error: true'
	} >"${workflow_path}"
}

module_record() {
	local directory="$1"
	local module_path="$2"
	local role="$3"
	local consumers_json="$4"
	local replacements_json="${5:-[]}"
	jq -cn \
		--arg directory "${directory}" \
		--arg module_path "${module_path}" \
		--arg role "${role}" \
		--argjson consumers "${consumers_json}" \
		--argjson replacements "${replacements_json}" \
		'{
		  directory: $directory,
		  modulePath: $module_path,
		  role: $role,
		  owner: "fixture owner",
		  supportedModes: ["standalone"],
		  validationTarget: "check-go-pr",
		  validationVariables: [],
		  packageScope: "fixture ./...",
		  prerequisites: [],
		  consumers: $consumers,
		  localReplacements: $replacements,
		  ciWorkflow: ".github/workflows/test.yml"
		}'
}

write_inventory() {
	local case_root="$1"
	local modules_json="$2"
	local workspaces_json="${3:-[]}"
	jq -n \
		--argjson modules "${modules_json}" \
		--argjson workspaces "${workspaces_json}" \
		'{
		  schemaVersion: 1,
		  repositoryModulePrefix: "example.test/insurance-hub/",
		  modules: $modules,
		  workspaces: $workspaces,
		  exclusions: [
		    {pathPattern: ".git/**", category: "repository-metadata", reason: "fixture metadata"},
		    {pathPattern: "**/testdata/**", category: "fixture", reason: "fixture-only module"}
		  ]
		}' >"${case_root}/go-module-topology.json"
	write_ci_workflow "${case_root}" "${modules_json}" "${workspaces_json}"
}

write_module() {
	local case_root="$1"
	local directory="$2"
	local module_path="$3"
	local package_name="$4"
	mkdir -p "${case_root}/${directory}"
	printf 'module %s\n\ngo 1.23\n' "${module_path}" >"${case_root}/${directory}/go.mod"
	: >"${case_root}/${directory}/go.sum"
	printf 'package %s\n\nconst Value = %q\n' "${package_name}" "${module_path}" \
		>"${case_root}/${directory}/${package_name}.go"
	cat >"${case_root}/${directory}/Makefile" <<'EOF'
.PHONY: check-go-pr
check-go-pr:
	@env -u GOFLAGS GOWORK=off GOPROXY=off GOSUMDB=off go list -mod=readonly ./... >/dev/null
EOF
}

new_single_case() {
	local name="$1"
	local case_root="${fixture_root}/${name}"
	local record
	mkdir -p "${case_root}"
	install_checker "${case_root}"
	write_module "${case_root}" "module" "example.test/insurance-hub/module" "module"
	record="$(module_record "module" "example.test/insurance-hub/module" "business-service" '["example.test/insurance-hub/module"]')"
	write_inventory "${case_root}" "[$record]"
	printf '%s' "${case_root}"
}

add_requirement() {
	local module_root="$1"
	local dependency_path="$2"
	local replacement_path="${3:-}"
	printf '\nrequire %s v0.0.0\n' "${dependency_path}" >>"${module_root}/go.mod"
	if [[ -n "${replacement_path}" ]]; then
		printf '\nreplace %s => %s\n' "${dependency_path}" "${replacement_path}" >>"${module_root}/go.mod"
	fi
}

add_import() {
	local source_path="$1"
	local package_name="$2"
	local import_path="$3"
	cat >"${source_path}" <<EOF
package ${package_name}

import dependency "${import_path}"

var Value = dependency.Value
EOF
}

case_count=0

expect_result() {
	local expectation="$1"
	local label="$2"
	local case_root="$3"
	local pattern="$4"
	shift 4
	local before_path="${fixture_root}/before-${case_count}"
	local after_path="${fixture_root}/after-${case_count}"
	local output_path="${fixture_root}/output-${case_count}"
	local status

	snapshot_fixture "${case_root}" "${before_path}"
	set +e
	"$@" >"${output_path}" 2>&1
	status=$?
	set -e
	snapshot_fixture "${case_root}" "${after_path}"

	if ! cmp -s "${before_path}" "${after_path}"; then
		diff -u "${before_path}" "${after_path}" >&2 || true
		fail "${label}: fixture changed during a read-only check"
	fi
	if [[ "${expectation}" == "pass" && ${status} -ne 0 ]]; then
		sed -n '1,160p' "${output_path}" >&2
		fail "${label}: expected success, got status ${status}"
	fi
	if [[ "${expectation}" == "fail" && ${status} -eq 0 ]]; then
		sed -n '1,160p' "${output_path}" >&2
		fail "${label}: expected a nonzero result"
	fi
	if [[ -n "${pattern}" ]] && ! grep -Eq -- "${pattern}" "${output_path}"; then
		sed -n '1,160p' "${output_path}" >&2
		fail "${label}: expected diagnostic did not match: ${pattern}"
	fi
	case_count=$((case_count + 1))
	printf 'controlled-case: %s | passed\n' "${label}"
}

expect_pass() {
	expect_result pass "$@"
}

expect_failure() {
	expect_result fail "$@"
}

case_root="$(new_single_case malformed-inventory)"
printf '{ malformed\n' >"${case_root}/go-module-topology.json"
expect_failure "malformed inventory" "${case_root}" 'inventory schema validation failed' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case duplicate-inventory)"
jq '.modules += [.modules[0]]' "${case_root}/go-module-topology.json" >"${case_root}/inventory.tmp"
mv "${case_root}/inventory.tmp" "${case_root}/go-module-topology.json"
expect_failure "duplicate inventory module" "${case_root}" 'module directories must be unique' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case missing-module)"
find "${case_root}/module/go.mod" -delete
expect_failure "missing inventory module" "${case_root}" 'missing inventory module manifest: module/go.mod' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case unexpected-module)"
mkdir -p "${case_root}/unexpected"
printf 'module example.test/insurance-hub/unexpected\n\ngo 1.23\n' >"${case_root}/unexpected/go.mod"
: >"${case_root}/unexpected/go.sum"
expect_failure "unexpected module" "${case_root}" 'unlisted developer-local manifest: unexpected/go.mod' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case module-path-mismatch)"
jq '.modules[0].modulePath = "example.test/insurance-hub/other"' \
	"${case_root}/go-module-topology.json" >"${case_root}/inventory.tmp"
mv "${case_root}/inventory.tmp" "${case_root}/go-module-topology.json"
expect_failure "module path mismatch" "${case_root}" \
	'module identity mismatch at module/go.mod: inventory=example.test/insurance-hub/other, actual=example.test/insurance-hub/module' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case unapproved-workspace)"
printf 'go 1.23\n\nuse ./module\n' >"${case_root}/go.work"
expect_failure "unapproved workspace" "${case_root}" 'unlisted developer-local manifest: go.work' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case unsupported-approved-workspace)"
workspace_record='[{"directory":".","owner":"fixture owner","use":["module"],"replacements":[],"validationTarget":"check","ciWorkflow":".github/workflows/test.yml"}]'
jq --argjson workspaces "${workspace_record}" '.workspaces = $workspaces' \
	"${case_root}/go-module-topology.json" >"${case_root}/inventory.tmp"
mv "${case_root}/inventory.tmp" "${case_root}/go-module-topology.json"
printf 'go 1.23\n\nuse ./module\n' >"${case_root}/go.work"
: >"${case_root}/go.work.sum"
expect_failure "approved workspace fails closed in topology" "${case_root}" \
	'approved workspaces are not supported by current topology validation; implement go.work membership, replacement, and validation-target enforcement before adding one' \
	make --no-print-directory -C "${case_root}" go-topology-check
expect_failure "approved workspace fails closed in aggregate" "${case_root}" \
	'approved workspaces are not supported by current topology validation; implement go.work membership, replacement, and validation-target enforcement before adding one' \
	make --no-print-directory -C "${case_root}" go-modules-check

case_root="$(new_single_case missing-owning-target)"
printf '.PHONY: other\nother:\n\t@true\n' >"${case_root}/module/Makefile"
expect_failure "missing owning target" "${case_root}" 'missing validation target check-go-pr in module/Makefile' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case missing-ci-module-trigger)"
grep -Fv -- "- 'module/**/*.go'" "${case_root}/.github/workflows/test.yml" \
	>"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "missing CI module trigger" "${case_root}" \
	'workflow .github/workflows/test.yml is missing pull_request path trigger for inventory module Go source module: module/\*\*/\*.go' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case broad-ci-module-trigger)"
sed "/module\/go.sum/a\\      - 'module/**'" "${case_root}/.github/workflows/test.yml" \
	>"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "broad CI module trigger" "${case_root}" \
	'workflow .github/workflows/test.yml contains forbidden pull_request path trigger for broad inventory module trigger module: module/\*\*' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case push-ci-event)"
sed "/  pull_request:/i\\  push:" "${case_root}/.github/workflows/test.yml" \
	>"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "push CI event" "${case_root}" \
	'workflow .github/workflows/test.yml must not run on push' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case missing-pr-merge-base)"
grep -Fv -- 'merge_base=$(git merge-base "$PR_BASE" "$PR_HEAD")' \
	"${case_root}/.github/workflows/test.yml" >"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "missing PR merge base" "${case_root}" \
	'workflow .github/workflows/test.yml is missing pull request merge-base resolution: merge_base=\$\(git merge-base "\$PR_BASE" "\$PR_HEAD"\)' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case endpoint-module-diff)"
sed 's/"$merge_base" "$PR_HEAD"/"$PR_BASE" "$PR_HEAD"/' \
	"${case_root}/.github/workflows/test.yml" >"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "endpoint module diff" "${case_root}" \
	'workflow .github/workflows/test.yml is missing merge-base-to-head module selection: git diff --name-only --diff-filter=ACDMRT -z "\$merge_base" "\$PR_HEAD"' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case base-tip-formatting)"
sed 's#FORMAT_BASE: ${{ steps.select.outputs.format_base }}#FORMAT_BASE: ${{ github.event.pull_request.base.sha }}#' \
	"${case_root}/.github/workflows/test.yml" >"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "base-tip formatting" "${case_root}" \
	'workflow .github/workflows/test.yml is missing selected-module validation contract: FORMAT_BASE: \${{ steps.select.outputs.format_base }}' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case missing-inline-selector-root)"
grep -Fv -- "module/*) selected[module]=1" "${case_root}/.github/workflows/test.yml" \
	>"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "missing inline selector root" "${case_root}" \
	'workflow .github/workflows/test.yml is missing inline selector root module: module/\*\)' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case missing-selected-validation)"
grep -Fv -- 'make -C "$MODULE_DIR" "$VALIDATION_TARGET"' \
	"${case_root}/.github/workflows/test.yml" \
	>"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "missing selected validation" "${case_root}" \
	'workflow .github/workflows/test.yml is missing selected-module validation contract: make -C "\$MODULE_DIR" "\$VALIDATION_TARGET"' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case cache-bypasses-version-check)"
sed 's/bootstrap-go-tools/verify-tools/' "${case_root}/.github/workflows/test.yml" \
	>"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "cache bypasses version check" "${case_root}" \
	'workflow .github/workflows/test.yml is missing selected-module validation contract: make -C "\$MODULE_DIR" bootstrap-go-tools' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case stale-coverage-report)"
grep -Fv -- 'rm -f -- "$COVERAGE_FILE"' "${case_root}/.github/workflows/test.yml" \
	>"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "stale coverage report" "${case_root}" \
	'workflow .github/workflows/test.yml is missing selected-module validation contract: rm -f -- "\$COVERAGE_FILE"' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case relative-coverage-report)"
sed 's#COVERAGE_FILE: ${{ github.workspace }}/${{ steps.select.outputs.coverage_file }}#COVERAGE_FILE: ${{ steps.select.outputs.coverage_file }}#' \
	"${case_root}/.github/workflows/test.yml" >"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "relative coverage report" "${case_root}" \
	'workflow .github/workflows/test.yml is missing selected-module validation contract: COVERAGE_FILE: \${{ github.workspace }}/\${{ steps.select.outputs.coverage_file }}' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case scaffold-coverage-upload)"
sed "s/steps.select.outputs.module_role != 'service-scaffold'/steps.select.outputs.module_dir != ''/" \
	"${case_root}/.github/workflows/test.yml" >"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "scaffold coverage upload" "${case_root}" \
	'workflow .github/workflows/test.yml is missing selected-module validation contract: steps.select.outputs.module_role != '\''service-scaffold'\''' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(new_single_case forbidden-java-setup)"
sed '/    steps:/a\      - uses: actions/setup-java@v5' \
	"${case_root}/.github/workflows/test.yml" >"${case_root}/workflow.tmp"
mv "${case_root}/workflow.tmp" "${case_root}/.github/workflows/test.yml"
expect_failure "forbidden Java setup" "${case_root}" \
	'workflow .github/workflows/test.yml contains forbidden non-Go or all-module execution: actions/setup-java@' \
	make --no-print-directory -C "${case_root}" go-topology-check

setup_edge_case() {
	local name="$1"
	local owner_role="$2"
	local owner_consumers="$3"
	local imported_package="$4"
	local case_root="${fixture_root}/${name}"
	local consumer_path='example.test/insurance-hub/consumer'
	local owner_path="example.test/insurance-hub/${name}-owner"
	local consumer_record owner_record modules_json replacement_json
	mkdir -p "${case_root}"
	install_checker "${case_root}"
	write_module "${case_root}" "consumer" "${consumer_path}" "consumer"
	write_module "${case_root}" "owner" "${owner_path}" "owner"
	if [[ "${imported_package}" == "internal/secret" ]]; then
		mkdir -p "${case_root}/owner/internal/secret"
		printf 'package secret\n\nconst Value = "secret"\n' >"${case_root}/owner/internal/secret/secret.go"
	fi
	add_requirement "${case_root}/consumer" "${owner_path}" "../owner"
	add_import "${case_root}/consumer/consumer.go" "consumer" "${owner_path}/${imported_package}"
	replacement_json="$(jq -cn --arg module_path "${owner_path}" \
		'[{modulePath: $module_path, replacementPath: "../owner", reason: "controlled boundary fixture"}]')"
	consumer_record="$(module_record "consumer" "${consumer_path}" "business-service" \
		"[\"${consumer_path}\"]" "${replacement_json}")"
	owner_record="$(module_record "owner" "${owner_path}" "${owner_role}" "${owner_consumers}")"
	modules_json="$(printf '%s\n%s\n' "${consumer_record}" "${owner_record}" | jq -s '.')"
	write_inventory "${case_root}" "${modules_json}"
	printf '%s' "${case_root}"
}

case_root="$(setup_edge_case approved-contract public-contract \
	'["example.test/insurance-hub/consumer"]' '')"
add_import "${case_root}/consumer/consumer.go" consumer 'example.test/insurance-hub/approved-contract-owner'
expect_pass "approved public contract" "${case_root}" 'go-boundary-check: passed 2 module graph' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(setup_edge_case sibling-private business-service \
	'["example.test/insurance-hub/sibling-private-owner"]' 'internal/secret')"
expect_failure "sibling private implementation" "${case_root}" \
	'rule=sibling-internal-import.*import=example.test/insurance-hub/sibling-private-owner/internal/secret|import=example.test/insurance-hub/sibling-private-owner/internal/secret.*rule=sibling-internal-import' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(setup_edge_case scaffold-runtime service-scaffold \
	'["example.test/insurance-hub/scaffold-runtime-owner"]' '')"
add_import "${case_root}/consumer/consumer.go" consumer 'example.test/insurance-hub/scaffold-runtime-owner'
expect_failure "scaffold runtime dependency" "${case_root}" \
	'rule=scaffold-runtime-dependency.*remediation=copy the scaffold structure|import=example.test/insurance-hub/scaffold-runtime-owner.*rule=scaffold-runtime-dependency' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="$(setup_edge_case undeclared-public public-contract \
	'["example.test/insurance-hub/undeclared-public-owner"]' '')"
add_import "${case_root}/consumer/consumer.go" consumer 'example.test/insurance-hub/undeclared-public-owner'
expect_failure "undeclared public consumer" "${case_root}" \
	'import=example.test/insurance-hub/undeclared-public-owner.*rule=unapproved-consumer' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="${fixture_root}/unknown-owner"
mkdir -p "${case_root}"
install_checker "${case_root}"
write_module "${case_root}" consumer 'example.test/insurance-hub/consumer' consumer
write_module "${case_root}" testdata/unknown 'example.test/insurance-hub/unknown' unknown
add_requirement "${case_root}/consumer" 'example.test/insurance-hub/unknown' '../testdata/unknown'
add_import "${case_root}/consumer/consumer.go" consumer 'example.test/insurance-hub/unknown'
replacement_json="$(jq -cn \
	'[{modulePath: "example.test/insurance-hub/unknown", replacementPath: "../testdata/unknown", reason: "controlled unknown-owner fixture"}]')"
record="$(module_record consumer 'example.test/insurance-hub/consumer' business-service \
	'["example.test/insurance-hub/consumer"]' "${replacement_json}")"
write_inventory "${case_root}" "[$record]"
expect_failure "unknown repository owner" "${case_root}" \
	'import=example.test/insurance-hub/unknown.*rule=unknown-repository-owner' \
	make --no-print-directory -C "${case_root}" go-topology-check

case_root="${fixture_root}/replacement-masking"
mkdir -p "${case_root}"
install_checker "${case_root}"
write_module "${case_root}" consumer 'example.test/insurance-hub/consumer' consumer
write_module "${case_root}" dependency 'example.test/insurance-hub/dependency' dependency
add_requirement "${case_root}/consumer" 'example.test/insurance-hub/dependency' '../dependency'
add_import "${case_root}/consumer/consumer.go" consumer 'example.test/insurance-hub/dependency'
consumer_record="$(module_record consumer 'example.test/insurance-hub/consumer' business-service \
	'["example.test/insurance-hub/consumer"]')"
owner_record="$(module_record dependency 'example.test/insurance-hub/dependency' public-contract \
	'["example.test/insurance-hub/consumer"]')"
modules_json="$(printf '%s\n%s\n' "${consumer_record}" "${owner_record}" | jq -s '.')"
write_inventory "${case_root}" "${modules_json}"
cat >>"${case_root}/Makefile" <<'EOF'
.PHONY: ordinary-replacement-resolution
ordinary-replacement-resolution:
	@cd consumer && GOWORK=off GOPROXY=off GOSUMDB=off go list -mod=readonly ./... >/dev/null
	@echo "ordinary local replacement resolution passed"
EOF
expect_pass "ordinary local replacement resolution control" "${case_root}" \
	'ordinary local replacement resolution passed' make --no-print-directory -C "${case_root}" ordinary-replacement-resolution
expect_failure "unapproved replacement topology contract" "${case_root}" \
	'module example.test/insurance-hub/consumer requires example.test/insurance-hub/dependency from unapproved filesystem replacement ../dependency' \
	make --no-print-directory -C "${case_root}" go-topology-check
expect_failure "unapproved replacement aggregate contract" "${case_root}" \
	'module example.test/insurance-hub/consumer requires example.test/insurance-hub/dependency from unapproved filesystem replacement ../dependency' \
	make --no-print-directory -C "${case_root}" go-modules-check

ambient_root="${fixture_root}/ambient-workspace"
case_root="${ambient_root}/repo"
mkdir -p "${case_root}"
install_checker "${case_root}"
write_module "${case_root}" consumer 'example.test/insurance-hub/consumer' consumer
write_module "${case_root}" dependency 'example.test/insurance-hub/dependency' dependency
add_requirement "${case_root}/consumer" 'example.test/insurance-hub/dependency'
add_import "${case_root}/consumer/consumer.go" consumer 'example.test/insurance-hub/dependency'
consumer_record="$(module_record consumer 'example.test/insurance-hub/consumer' business-service \
	'["example.test/insurance-hub/consumer"]')"
owner_record="$(module_record dependency 'example.test/insurance-hub/dependency' public-contract \
	'["example.test/insurance-hub/consumer"]')"
modules_json="$(printf '%s\n%s\n' "${consumer_record}" "${owner_record}" | jq -s '.')"
write_inventory "${case_root}" "${modules_json}"
cat >"${ambient_root}/go.work" <<'EOF'
go 1.23

use (
	./repo/consumer
	./repo/dependency
)
EOF
cat >>"${case_root}/Makefile" <<'EOF'
.PHONY: ambient-workspace-resolution
ambient-workspace-resolution:
	@cd consumer && GOWORK="$$(cd ../.. && pwd -P)/go.work" GOPROXY=off GOSUMDB=off go list -mod=readonly ./... >/dev/null
	@echo "ambient workspace resolution passed"
EOF
expect_pass "ambient workspace resolution control" "${ambient_root}" \
	'ambient workspace resolution passed' make --no-print-directory -C "${case_root}" ambient-workspace-resolution
expect_failure "standalone rejects ambient workspace masking" "${ambient_root}" \
	'consumer-package=example.test/insurance-hub/consumer import=example.test/insurance-hub/dependency.*rule=incomplete-package-graph' \
	env GOPROXY=off GOSUMDB=off make --no-print-directory -C "${case_root}" go-topology-check

readonly checkout_after="${fixture_root}/checkout-after"
snapshot_checkout "${checkout_after}"
if ! cmp -s "${checkout_before}" "${checkout_after}"; then
	diff -u "${checkout_before}" "${checkout_after}" >&2 || true
	fail "real checkout source, topology, or manifest bytes changed"
fi

printf 'go-topology-test: passed %d controlled cases; fixtures and checkout unchanged\n' \
	"${case_count}"
