#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
	printf '%s\n' 'usage: runtime_check.sh <python-executable>' >&2
	exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd -P)
IFS= read -r expected_version < "$repository_root/.python-version"
python=$1

if [ ! -x "$python" ]; then
	printf 'ERROR: selected Python is missing or not executable: %s\n' "$python" >&2
	printf "Run 'make python-toolchain-setup' or set PYTHON to a verified CPython %s executable.\n" \
		"$expected_version" >&2
	exit 1
fi

if ! PYTHONDONTWRITEBYTECODE=1 "$python" -I -c \
	'import platform, sys; expected = ("CPython", sys.argv[1]); actual = (platform.python_implementation(), platform.python_version()); raise SystemExit(0 if actual == expected else "ERROR: selected Python must be %s %s, got %s %s at %s" % (*expected, *actual, sys.executable))' \
	"$expected_version"; then
	printf "Run 'make python-toolchain-setup' or set PYTHON to a verified CPython %s executable.\n" \
		"$expected_version" >&2
	exit 1
fi
