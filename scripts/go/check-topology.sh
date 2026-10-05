#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly script_dir
default_repo_root="$(cd -- "${script_dir}/../.." && pwd -P)"
if [[ -n "${GO_TOPOLOGY_FIXTURE_ROOT:-}" || -n "${GO_TOPOLOGY_FIXTURE_INVENTORY:-}" ]]; then
	[[ "${GO_TOPOLOGY_TEST_MODE:-0}" == "1" ]] || {
		printf 'go-topology-check: fixture inputs require GO_TOPOLOGY_TEST_MODE=1\n' >&2
		exit 1
	}
	[[ -n "${GO_TOPOLOGY_FIXTURE_ROOT:-}" && -n "${GO_TOPOLOGY_FIXTURE_INVENTORY:-}" ]] || {
		printf 'go-topology-check: fixture root and inventory must be provided together\n' >&2
		exit 1
	}
	repo_root="$(cd -- "${GO_TOPOLOGY_FIXTURE_ROOT}" && pwd -P)"
	[[ -f "${repo_root}/.go-topology-test-fixture" ]] || {
		printf 'go-topology-check: fixture root is missing its test marker\n' >&2
		exit 1
	}
	inventory_path="$(cd -- "$(dirname -- "${GO_TOPOLOGY_FIXTURE_INVENTORY}")" && pwd -P)/$(basename -- "${GO_TOPOLOGY_FIXTURE_INVENTORY}")"
	case "${inventory_path}" in
		"${repo_root}"/*) ;;
		*)
			printf 'go-topology-check: fixture inventory must be inside the fixture root\n' >&2
			exit 1
			;;
	esac
else
	repo_root="${default_repo_root}"
	inventory_path="${repo_root}/go-module-topology.json"
fi
readonly repo_root
readonly inventory_path

fail() {
	printf 'go-topology-check: %s\n' "$*" >&2
	exit 1
}

command -v jq >/dev/null 2>&1 || fail "jq >= 1.6 is required"
command -v go >/dev/null 2>&1 || fail "Go is required to inspect effective module metadata"
jq_version="$(jq --version | sed 's/^jq-//')"
jq_major="${jq_version%%.*}"
jq_minor="${jq_version#*.}"
jq_minor="${jq_minor%%[^0-9]*}"
if ! [[ "${jq_major}" =~ ^[0-9]+$ && "${jq_minor}" =~ ^[0-9]+$ ]]; then
	fail "could not parse jq version: ${jq_version}"
fi
if ((jq_major < 1 || (jq_major == 1 && jq_minor < 6))); then
	fail "jq >= 1.6 is required; found ${jq_version}"
fi

test -f "${inventory_path}" || fail "missing inventory: go-module-topology.json"

jq -e '
  def fail($message): error("go-module-topology.json: " + $message);
  def exact_keys($expected; $path):
    ((keys_unsorted - $expected) + ($expected - keys_unsorted)) as $difference
    | if ($difference | length) == 0 then .
      else fail($path + " has missing or unknown fields: " + ($difference | join(", ")))
      end;
  def nonempty_string($path):
    if type == "string" and length > 0 and (test("[\\t\\r\\n]") | not) then .
    else fail($path + " must be a non-empty single-line string")
    end;
  def relative_directory($path; $allow_root):
    nonempty_string($path)
    | if . == "." and $allow_root then .
      elif startswith("/") or endswith("/") or contains("//")
        or any(split("/")[]; . == "." or . == "..")
      then fail($path + " must be a normalized repository-relative directory")
      else . end;
  def string_array($path; $allow_empty):
    if type != "array" then fail($path + " must be an array")
    elif ($allow_empty | not) and length == 0 then fail($path + " must not be empty")
    elif any(.[]; (type != "string") or length == 0 or test("[\\t\\r\\n]")) then
      fail($path + " must contain non-empty single-line strings")
    elif (unique | length) != length then fail($path + " contains duplicates")
    else . end;

  if type != "object" then fail("root must be an object") else . end
  | exact_keys(["schemaVersion", "repositoryModulePrefix", "modules", "workspaces", "exclusions"]; "root")
  | if .schemaVersion != 1 then fail("schemaVersion must equal 1") else . end
  | .repositoryModulePrefix |= nonempty_string("repositoryModulePrefix")
  | if (.repositoryModulePrefix | test("^[A-Za-z0-9.-]+(/[A-Za-z0-9._~-]+)*/$") | not)
    then fail("repositoryModulePrefix must be a slash-terminated module path prefix") else . end
  | if (.modules | type) != "array" or (.modules | length) == 0
    then fail("modules must be a non-empty array") else . end
  | if (.workspaces | type) != "array" then fail("workspaces must be an array") else . end
  | if (.exclusions | type) != "array" then fail("exclusions must be an array") else . end
  | .modules |= (to_entries | map(
      .key as $index | .value
      | if type != "object" then fail("modules[" + ($index | tostring) + "] must be an object") else . end
      | exact_keys([
          "directory", "modulePath", "role", "owner", "supportedModes",
          "validationTarget", "validationVariables", "packageScope", "prerequisites",
          "consumers", "localReplacements", "ciWorkflow"
        ]; "modules[" + ($index | tostring) + "]")
      | .directory |= relative_directory("modules[" + ($index | tostring) + "].directory"; true)
      | .modulePath |= nonempty_string("modules[" + ($index | tostring) + "].modulePath")
      | .role |= nonempty_string("modules[" + ($index | tostring) + "].role")
      | .role as $role
      | if (["service-scaffold", "business-service", "public-contract", "public-shared", "tool"] | index($role)) == null
        then fail("modules[" + ($index | tostring) + "].role is unknown: " + $role) else . end
      | .owner |= nonempty_string("modules[" + ($index | tostring) + "].owner")
      | .supportedModes |= string_array("modules[" + ($index | tostring) + "].supportedModes"; false)
      | if any(.supportedModes[]; . != "standalone" and . != "workspace")
        then fail("modules[" + ($index | tostring) + "].supportedModes contains an unknown mode") else . end
      | .validationTarget |= nonempty_string("modules[" + ($index | tostring) + "].validationTarget")
      | if (.validationTarget | test("^[A-Za-z0-9][A-Za-z0-9._-]*$") | not)
        then fail("modules[" + ($index | tostring) + "].validationTarget is malformed") else . end
      | .validationVariables |= string_array("modules[" + ($index | tostring) + "].validationVariables"; true)
      | if any(.validationVariables[]; test("^[A-Z][A-Z0-9_]*$") | not)
        then fail("modules[" + ($index | tostring) + "].validationVariables contains a malformed name") else . end
      | .packageScope |= nonempty_string("modules[" + ($index | tostring) + "].packageScope")
      | .prerequisites |= string_array("modules[" + ($index | tostring) + "].prerequisites"; true)
      | .consumers |= string_array("modules[" + ($index | tostring) + "].consumers"; false)
      | if (.localReplacements | type) != "array"
        then fail("modules[" + ($index | tostring) + "].localReplacements must be an array") else . end
      | .localReplacements |= (to_entries | map(
          .key as $replacement_index | .value
          | if type != "object" then
              fail("modules[" + ($index | tostring) + "].localReplacements[" + ($replacement_index | tostring) + "] must be an object")
            else . end
          | exact_keys(["modulePath", "replacementPath", "reason"];
              "modules[" + ($index | tostring) + "].localReplacements[" + ($replacement_index | tostring) + "]")
          | .modulePath |= nonempty_string("local replacement modulePath")
          | .replacementPath |= nonempty_string("local replacement replacementPath")
          | .reason |= nonempty_string("local replacement reason")
        ))
      | .ciWorkflow |= relative_directory("modules[" + ($index | tostring) + "].ciWorkflow"; false)
    ))
  | .workspaces |= (to_entries | map(
      .key as $index | .value
      | if type != "object" then fail("workspaces[" + ($index | tostring) + "] must be an object") else . end
      | exact_keys(["directory", "owner", "use", "replacements", "validationTarget", "ciWorkflow"];
          "workspaces[" + ($index | tostring) + "]")
      | .directory |= relative_directory("workspaces[" + ($index | tostring) + "].directory"; true)
      | .owner |= nonempty_string("workspaces[" + ($index | tostring) + "].owner")
      | .use |= string_array("workspaces[" + ($index | tostring) + "].use"; false)
      | .replacements |= string_array("workspaces[" + ($index | tostring) + "].replacements"; true)
      | .validationTarget |= nonempty_string("workspaces[" + ($index | tostring) + "].validationTarget")
      | .ciWorkflow |= nonempty_string("workspaces[" + ($index | tostring) + "].ciWorkflow")
    ))
  | .exclusions |= (to_entries | map(
      .key as $index | .value
      | if type != "object" then fail("exclusions[" + ($index | tostring) + "] must be an object") else . end
      | exact_keys(["pathPattern", "category", "reason"]; "exclusions[" + ($index | tostring) + "]")
      | .pathPattern |= nonempty_string("exclusions[" + ($index | tostring) + "].pathPattern")
      | if (.pathPattern | startswith("/")) or (.pathPattern | contains(".."))
        or (.pathPattern | endswith("/**") | not)
        or (.pathPattern | test("^(\\*\\*/)?[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*/\\*\\*$") | not)
        then fail("exclusions[" + ($index | tostring) + "].pathPattern must be a safe relative subtree pattern ending in /**")
        else . end
      | .category |= nonempty_string("exclusions[" + ($index | tostring) + "].category")
      | .category as $category
      | if (["repository-metadata", "local-ticket-artifact", "tool-cache", "dependency-tree", "fixture", "generated-copy"] | index($category)) == null
        then fail("exclusions[" + ($index | tostring) + "].category is unknown: " + $category) else . end
      | .reason |= nonempty_string("exclusions[" + ($index | tostring) + "].reason")
    ))
  | .repositoryModulePrefix as $repository_module_prefix
  | if ([.modules[].directory] | unique | length) != (.modules | length)
    then fail("module directories must be unique") else . end
  | if ([.modules[].modulePath] | unique | length) != (.modules | length)
    then fail("module paths must be unique") else . end
  | if any(.modules[]; (.modulePath | startswith($repository_module_prefix)) | not)
    then fail("every module path must start with repositoryModulePrefix") else . end
  | if ([.workspaces[].directory] | unique | length) != (.workspaces | length)
    then fail("workspace directories must be unique") else . end
  | if ([.exclusions[].pathPattern] | unique | length) != (.exclusions | length)
    then fail("exclusion path patterns must be unique") else . end
' "${inventory_path}" >/dev/null || fail "inventory schema validation failed"

mapfile -t exclusions < <(jq -r '.exclusions[] | [.pathPattern, .category] | @tsv' "${inventory_path}")

classify_exclusion() {
	local path="$1"
	local record pattern category directory
	for record in "${exclusions[@]}"; do
		IFS=$'\t' read -r pattern category <<<"${record}"
		if [[ "${pattern:0:3}" == "**/" ]]; then
			directory="${pattern#"**/"}"
			directory="${directory%"/**"}"
			if [[ "${path}" == "${directory}/"* || "${path}" == *"/${directory}/"* ]]; then
				printf '%s' "${category}"
				return 0
			fi
		else
			directory="${pattern%"/**"}"
			if [[ "${path}" == "${directory}/"* ]]; then
				printf '%s' "${category}"
				return 0
			fi
		fi
	done
	return 1
}

declare -a relevant_manifests=()
declare -a excluded_manifests=()
while IFS= read -r -d '' absolute_path; do
	relative_path="${absolute_path#"${repo_root}/"}"
	if category="$(classify_exclusion "${relative_path}")"; then
		excluded_manifests+=("${relative_path}"$'\t'"${category}")
	else
		relevant_manifests+=("${relative_path}")
	fi
done < <(
	find -P "${repo_root}" -type f \
		\( -name go.mod -o -name go.sum -o -name go.work -o -name go.work.sum \) \
		-print0 | sort -z
)

for record in "${excluded_manifests[@]}"; do
	IFS=$'\t' read -r path category <<<"${record}"
	printf 'excluded: %s [%s]\n' "${path}" "${category}"
done

declare -A discovered=()
for path in "${relevant_manifests[@]}"; do
	discovered["${path}"]=1
done

declare -A expected=()
declare -a errors=()
while IFS=$'\t' read -r directory module_path; do
	if [[ "${directory}" == "." ]]; then
		mod_file="go.mod"
		sum_file="go.sum"
	else
		mod_file="${directory}/go.mod"
		sum_file="${directory}/go.sum"
	fi
	expected["${mod_file}"]=1
	expected["${sum_file}"]=1
	if [[ ! -f "${repo_root}/${mod_file}" ]]; then
		errors+=("missing inventory module manifest: ${mod_file}")
		continue
	fi
	if [[ ! -f "${repo_root}/${sum_file}" ]]; then
		errors+=("missing inventory module checksum: ${sum_file}")
	fi
	validation_target="$(jq -r --arg directory "${directory}" '.modules[] | select(.directory == $directory) | .validationTarget' "${inventory_path}")"
	if [[ "${directory}" == "." ]]; then
		module_makefile="Makefile"
	else
		module_makefile="${directory}/Makefile"
	fi
	if [[ ! -f "${repo_root}/${module_makefile}" ]]; then
		errors+=("missing owning Makefile for ${directory}: ${module_makefile}")
	elif ! grep -Eq "^${validation_target}([[:space:]]*|[^A-Za-z0-9._-].*):" "${repo_root}/${module_makefile}"; then
		errors+=("missing validation target ${validation_target} in ${module_makefile}")
	fi
	ci_workflow="$(jq -r --arg directory "${directory}" '.modules[] | select(.directory == $directory) | .ciWorkflow' "${inventory_path}")"
	if [[ ! -f "${repo_root}/${ci_workflow}" ]]; then
		errors+=("missing CI workflow for ${directory}: ${ci_workflow}")
	fi
	actual_module_path="$(sed -n 's/^module[[:space:]][[:space:]]*//p' "${repo_root}/${mod_file}")"
	if [[ -z "${actual_module_path}" ]]; then
		errors+=("missing module directive: ${mod_file}")
	elif [[ "${actual_module_path}" != "${module_path}" ]]; then
		errors+=("module identity mismatch at ${mod_file}: inventory=${module_path}, actual=${actual_module_path}")
	fi

	set +e
	metadata="$(env -u GOFLAGS GOWORK=off go mod edit -json "${repo_root}/${mod_file}")"
	metadata_status=$?
	set -e
	if ((metadata_status != 0)) || [[ -z "${metadata}" ]] || \
		! jq -e 'type == "object"' <<<"${metadata}" >/dev/null
	then
		errors+=("could not inspect effective manifest metadata for ${module_path} at ${mod_file}")
		continue
	fi

	while IFS=$'\t' read -r required_module replacement_path; do
		if ! jq -e \
			--arg directory "${directory}" \
			--arg required_module "${required_module}" \
			--arg replacement_path "${replacement_path}" \
			'.modules[] | select(.directory == $directory) | .localReplacements[]?
			 | select(.modulePath == $required_module and .replacementPath == $replacement_path)' \
			"${inventory_path}" >/dev/null; then
			errors+=("module ${module_path} requires ${required_module} from unapproved filesystem replacement ${replacement_path}; standalone portability is not proven")
		fi
	done < <(
		jq -r '.Replace[]? | select((.New.Version // "") == "") | [.Old.Path, .New.Path] | @tsv' \
			<<<"${metadata}"
	)

	while IFS=$'\t' read -r required_module replacement_path; do
		if ! jq -e \
			--arg required_module "${required_module}" \
			--arg replacement_path "${replacement_path}" \
			'.Replace[]?
			 | select(.Old.Path == $required_module and .New.Path == $replacement_path
			   and (.New.Version // "") == "")' \
			<<<"${metadata}" >/dev/null; then
			errors+=("module ${module_path} inventory approves missing filesystem replacement ${required_module} => ${replacement_path}")
		fi
	done < <(
		jq -r --arg directory "${directory}" \
			'.modules[] | select(.directory == $directory) | .localReplacements[]?
			 | [.modulePath, .replacementPath] | @tsv' "${inventory_path}"
	)
done < <(jq -r '.modules[] | [.directory, .modulePath] | @tsv' "${inventory_path}")

while IFS=$'\t' read -r directory; do
	if [[ "${directory}" == "." ]]; then
		work_file="go.work"
	else
		work_file="${directory}/go.work"
	fi
	expected["${work_file}"]=1
	if [[ ! -f "${repo_root}/${work_file}" ]]; then
		errors+=("missing inventory workspace manifest: ${work_file}")
	fi
done < <(jq -r '.workspaces[] | [.directory] | @tsv' "${inventory_path}")

for path in "${relevant_manifests[@]}"; do
	if [[ -z "${expected[${path}]+present}" ]]; then
		if git -C "${repo_root}" ls-files --error-unmatch -- "${path}" >/dev/null 2>&1; then
			errors+=("unlisted repository manifest: ${path}")
		else
			errors+=("unlisted developer-local manifest: ${path}")
		fi
	fi
done

for path in "${!expected[@]}"; do
	if [[ -z "${discovered[${path}]+present}" ]]; then
		errors+=("inventory path was not discovered: ${path}")
	fi
done

if ((${#errors[@]} > 0)); then
	for message in "${errors[@]}"; do
		printf 'go-topology-check: %s\n' "${message}" >&2
	done
	exit 1
fi

readonly boundary_checker="${script_dir}/check-boundaries.sh"
test -x "${boundary_checker}" || fail "missing executable boundary checker: scripts/go/check-boundaries.sh"
readonly ci_checker="${script_dir}/check-ci-coverage.sh"
test -x "${ci_checker}" || fail "missing executable CI coverage checker: scripts/go/check-ci-coverage.sh"
"${ci_checker}" "${repo_root}" "${inventory_path}"
"${boundary_checker}"

printf 'Go module topology: %d module(s), %d approved workspace(s)\n' \
	"$(jq '.modules | length' "${inventory_path}")" \
	"$(jq '.workspaces | length' "${inventory_path}")"
jq -r '
  .modules[]
  | "module: \(.modulePath)"
    + " | directory=\(.directory)"
    + " | role=\(.role)"
    + " | owner=\(.owner)"
    + " | modes=\(.supportedModes | join(","))"
    + " | validation=\(.validationTarget)"
    + " | variables=\(.validationVariables | if length == 0 then "none" else join(",") end)"
    + " | scope=\(.packageScope)"
    + " | prerequisites=\(.prerequisites | if length == 0 then "none" else join(";") end)"
    + " | consumers=\(.consumers | join(","))"
    + " | replacements=\(.localReplacements | if length == 0 then "none" else map(.modulePath + "=>" + .replacementPath) | join(",") end)"
    + " | workspace=none"
    + " | ci=\(.ciWorkflow)"
' "${inventory_path}"
if [[ "$(jq '.workspaces | length' "${inventory_path}")" == "0" ]]; then
	printf 'workspaces: none approved\n'
fi
printf 'discovery: %s\n' "$(printf '%s\n' "${relevant_manifests[@]}" | paste -sd ',' -)"
