#!/usr/bin/env bash

set -euo pipefail

if (($# != 2)); then
	printf 'usage: %s <coverage-profile> <minimum-percent>\n' "$0" >&2
	exit 2
fi

profile=$1
minimum=$2

if [[ ! -f "$profile" ]]; then
	printf 'coverage-check: profile not found: %s\n' "$profile" >&2
	exit 1
fi
if [[ ! "$minimum" =~ ^(100|[0-9]{1,2})$ ]]; then
	printf 'coverage-check: minimum must be an integer from 0 to 100\n' >&2
	exit 2
fi

awk -v minimum="$minimum" '
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

    # Match the generated and test-support paths excluded by codecov.yml.
    if ($1 ~ /\/services\/product-service\/gen\// ||
        $1 ~ /\/services\/product-service\/internal\/postgres\/dbgen\// ||
        $1 ~ /\/services\/product-service\/internal\/testing\//) {
      next
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
