#!/bin/sh

set -eu

module_root=$(
	unset CDPATH
	cd -- "$(dirname -- "$0")/.." && pwd
)
make_command=${MAKE_COMMAND:-make}

source_module_pattern=github\.com/igor-baiborodine/insurance-hub/templates/go-service
copy_module=github.com/igor-baiborodine/insurance-hub/services/example-copy
source_directory=templates/go-service
copy_directory=services/example-copy
source_service=go-service
copy_service=example-copy
source_proto_path=scaffold/v1
copy_proto_path=examplecopy/v1
copy_proto_package=examplecopy.v1
source_go_package=scaffoldv1
copy_go_package=examplecopyv1

temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/go-service-copy.XXXXXX")
case "$temporary_root" in
	"${TMPDIR:-/tmp}"/go-service-copy.*) ;;
	*)
		echo "check-copy: refusing unsafe temporary path: $temporary_root" >&2
		exit 1
		;;
esac

cleanup() {
	rm -rf -- "$temporary_root"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

copy_root=$temporary_root/services/example-copy
source_inventory=$temporary_root/source-before.sha256
source_inventory_after=$temporary_root/source-after.sha256
copy_inventory=$temporary_root/copy-before-checks.sha256
copy_inventory_after=$temporary_root/copy-after-checks.sha256
generated_inventory=$temporary_root/generated-files.txt

inventory_tree() {
	root=$1
	output=$2
	(
		cd "$root"
		find . -type f ! -path './.tools/*' -print0 |
			sort -z |
			xargs -0 -r sha256sum
	) >"$output"
}

replace_identity() {
	old=$1
	new=$2
	find "$copy_root" -type f \
		! -path "$copy_root/COPYING.md" \
		! -path "$copy_root/.tools/*" \
		-print0 |
		xargs -0 -r sed -i "s|$old|$new|g"
}

remove_scaffold_only_copy_surface() {
	sed -i 's/^\([[:space:]]*\)check-copy run$/\1run/' "$copy_root/Makefile"
	sed -i '/^check-copy: verify-tools$/,+1d' "$copy_root/Makefile"
	rm -- "$copy_root/scripts/check-copy.sh"

	sed -i \
		-e '/^| `make check-copy`/d' \
		-e '/^make check-copy$/d' \
		"$copy_root/README.md"
	awk '
		/^### Root scaffold delegates$/ {
			print "### Repository onboarding"
			print ""
			print "The scaffold-only `go-scaffold-*` root delegates and"
			print "`.github/workflows/go-scaffold.yml` do not cover this copied module. Run module-owned"
			print "Make targets from this directory. Add root and CI coverage only through the repository"
			print "topology and onboarding workflow owned by issue 123."
			skipping = 1
			next
		}
		skipping && /^## Create an independently owned service$/ { skipping = 0 }
		!skipping { print }
	' "$copy_root/README.md" >"$copy_root/README.md.copy"
	mv -- "$copy_root/README.md.copy" "$copy_root/README.md"
}

inventory_tree "$module_root" "$source_inventory"

mkdir -p "$copy_root"
(
	cd "$module_root"
	tar \
		--exclude='./.git' \
		--exclude='./.git/**' \
		--exclude='./.tools' \
		--exclude='./.tools/**' \
		-cf - .
) | (
	cd "$copy_root"
	tar -xf -
)

rm -rf -- "$copy_root/gen"

mkdir -p \
	"$copy_root/api/$copy_proto_path" \
	"$copy_root/testdata/contract-baseline/api/$copy_proto_path"
mv \
	"$copy_root/api/$source_proto_path/example_service.proto" \
	"$copy_root/api/$copy_proto_path/example_service.proto"
mv \
	"$copy_root/testdata/contract-baseline/api/$source_proto_path/example_service.proto" \
	"$copy_root/testdata/contract-baseline/api/$copy_proto_path/example_service.proto"
rmdir \
	"$copy_root/api/scaffold/v1" \
	"$copy_root/api/scaffold" \
	"$copy_root/testdata/contract-baseline/api/scaffold/v1" \
	"$copy_root/testdata/contract-baseline/api/scaffold"

replace_identity "$source_module_pattern" "$copy_module"
replace_identity "$source_directory" "$copy_directory"
replace_identity "$source_proto_path" "$copy_proto_path"
replace_identity 'scaffold\.v1' "$copy_proto_package"
replace_identity "$source_go_package" "$copy_go_package"
replace_identity "$source_service" "$copy_service"
sed -i 's/^# Go service scaffold$/# Example copy service/' "$copy_root/README.md"
remove_scaffold_only_copy_surface

if grep -R -n -E \
	--exclude=COPYING.md \
	--exclude=check-copy.sh \
	--exclude-dir=.tools \
	'github\.com/igor-baiborodine/insurance-hub/templates/go-service|templates/go-service|go-service|scaffold\.v1|scaffold_v1|scaffoldv1|scaffold/v1' \
	"$copy_root"
then
	echo 'check-copy: stale active scaffold identity found' >&2
	exit 1
fi

test ! -e "$copy_root/go.work"
test ! -e "$copy_root/.git"
test ! -e "$copy_root/scripts/check-copy.sh"
if grep -q '^check-copy:' "$copy_root/Makefile" ||
	grep -q 'check-copy' "$copy_root/Makefile" ||
	grep -Fq '`make check-copy`' "$copy_root/README.md"; then
	echo 'check-copy: copied service retained scaffold-only check-copy tooling' >&2
	exit 1
fi
if grep -Fq 'make go-scaffold-' "$copy_root/README.md" ||
	grep -Fq 'Go service scaffold CI' "$copy_root/README.md" ||
	grep -Fq 'when this module, the workflow, or the root Makefile changes' \
		"$copy_root/README.md"; then
	echo 'check-copy: copied README claims scaffold-only root or CI coverage' >&2
	exit 1
fi
grep -Fq 'do not cover this copied module' "$copy_root/README.md"
grep -Fq 'onboarding workflow owned by issue 123' "$copy_root/README.md"
if grep -n -E '^replace([[:space:]]|$)' "$copy_root/go.mod"; then
	echo 'check-copy: filesystem replacement found in copied go.mod' >&2
	exit 1
fi

test "$(sed -n '1s/^module //p' "$copy_root/go.mod")" = "$copy_module"
test "$(sed -n 's/^const DefaultServiceName = "\([^"]*\)"/\1/p' \
	"$copy_root/internal/config/config.go")" = "$copy_service"
test -f "$copy_root/api/$copy_proto_path/example_service.proto"
test -f "$copy_root/testdata/contract-baseline/api/$copy_proto_path/example_service.proto"
grep -Fqx "package $copy_proto_package;" \
	"$copy_root/api/$copy_proto_path/example_service.proto"
grep -Fq "$copy_module/gen/$copy_proto_path;$copy_go_package" \
	"$copy_root/api/$copy_proto_path/example_service.proto"
grep -Fqx "package $copy_proto_package;" \
	"$copy_root/testdata/contract-baseline/api/$copy_proto_path/example_service.proto"
grep -Fq "$copy_module/gen/$copy_proto_path;$copy_go_package" \
	"$copy_root/testdata/contract-baseline/api/$copy_proto_path/example_service.proto"

echo 'check-copy: bootstrapping renamed copy tools'
"$make_command" -C "$copy_root" bootstrap-tools
echo 'check-copy: refreshing renamed copy Protobuf dependencies'
"$make_command" -C "$copy_root" update-proto-deps
echo 'check-copy: generating renamed copy bindings'
"$make_command" -C "$copy_root" gen-proto
echo 'check-copy: refreshing renamed copy Go dependencies'
"$make_command" -C "$copy_root" update-deps
echo 'check-copy: formatting renamed copy handwritten Go files'
"$make_command" -C "$copy_root" format FORMAT_SCOPE=all

(
	cd "$copy_root"
	find gen -type f -print | sort
) >"$generated_inventory"
if ! printf '%s\n' \
	"gen/$copy_proto_path/example_service.pb.go" \
	"gen/$copy_proto_path/example_service_grpc.pb.go" |
	cmp -s - "$generated_inventory"
then
	echo 'check-copy: generated output path set differs from the expected renamed paths' >&2
	cat "$generated_inventory" >&2
	exit 1
fi

inventory_tree "$copy_root" "$copy_inventory"

echo 'check-copy: running renamed copy non-race tests'
"$make_command" -C "$copy_root" test
echo 'check-copy: running renamed copy aggregate checks'
"$make_command" -C "$copy_root" check FORMAT_SCOPE=all

inventory_tree "$copy_root" "$copy_inventory_after"
if ! cmp -s "$copy_inventory" "$copy_inventory_after"; then
	echo 'check-copy: normal validation changed copied module content' >&2
	diff -u "$copy_inventory" "$copy_inventory_after" >&2 || true
	exit 1
fi

inventory_tree "$module_root" "$source_inventory_after"
if ! cmp -s "$source_inventory" "$source_inventory_after"; then
	echo 'check-copy: renamed-copy proof changed source module content' >&2
	diff -u "$source_inventory" "$source_inventory_after" >&2 || true
	exit 1
fi

echo 'check-copy: renamed copy passed identity, generation, test, and aggregate Make checks'
