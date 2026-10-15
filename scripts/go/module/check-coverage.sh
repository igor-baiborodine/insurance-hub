#!/usr/bin/env bash

set -euo pipefail

if (($# < 3)); then
	printf 'usage: %s <coverage-profile> <minimum-percent> <module-path> [excluded-relative-path ...]\n' "$0" >&2
	exit 2
fi

profile=$1
minimum=$2
module_path=$3
shift 3

if [[ ! -f "$profile" ]]; then
	printf 'coverage-check: profile not found: %s\n' "$profile" >&2
	exit 1
fi
if [[ ! "$minimum" =~ ^(100|[0-9]{1,2})$ ]]; then
	printf 'coverage-check: minimum must be an integer from 0 to 100\n' >&2
	exit 2
fi
if [[ -z "$module_path" || "$module_path" == *[[:space:]]* || "$module_path" == */ ]]; then
	printf 'coverage-check: module path must be a non-empty normalized value\n' >&2
	exit 2
fi

exclusions=
for exclusion in "$@"; do
	case "$exclusion" in
	'' | /* | */ | *//* | . | .. | ./* | */./* | */. | ../* | */../* | */..)
		printf 'coverage-check: invalid excluded relative path: %s\n' "$exclusion" >&2
		exit 2
		;;
	esac
	exclusions+="${exclusion}"$'\n'
done

awk -v minimum="$minimum" -v module_path="$module_path" -v exclusions="$exclusions" '
  BEGIN {
    exclusion_count = split(exclusions, raw_exclusions, "\n")
    for (exclusion_index = 1; exclusion_index <= exclusion_count; exclusion_index++) {
      if (raw_exclusions[exclusion_index] != "") {
        excluded[raw_exclusions[exclusion_index]] = 1
      }
    }
  }
  NR == 1 {
    if ($0 !~ /^mode: (set|count|atomic)$/) {
      print "coverage-check: invalid coverage profile header" > "/dev/stderr"
      invalid = 1
      exit
    }
    next
  }
  {
    if (NF != 3 || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+$/) {
      printf "coverage-check: invalid profile entry at line %d\n", NR > "/dev/stderr"
      invalid = 1
      exit
    }

    source_path = $1
    sub(/:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$/, "", source_path)
    module_prefix = module_path "/"
    if (index(source_path, module_prefix) != 1) {
      printf "coverage-check: profile entry is outside module %s: %s\n", \
        module_path, source_path > "/dev/stderr"
      invalid = 1
      exit
    }
    relative_path = substr(source_path, length(module_prefix) + 1)
    for (excluded_path in excluded) {
      if (relative_path == excluded_path ||
          index(relative_path, excluded_path "/") == 1) {
        next
      }
    }

    total += $2
    if ($3 > 0) {
      covered += $2
    }
  }
  END {
    if (invalid) {
      exit 1
    }
    if (total == 0) {
      print "coverage-check: no included statements in profile" > "/dev/stderr"
      exit 1
    }

    printf "coverage-check: %.2f%% (%d/%d statements; minimum %d%%)\n", \
      100 * covered / total, covered, total, minimum
    if (100 * covered < minimum * total) {
      print "coverage-check: below minimum" > "/dev/stderr"
      exit 1
    }
  }
' "$profile"
