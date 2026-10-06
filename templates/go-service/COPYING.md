# Create a renamed service from the Go scaffold

This guide travels with the template and intentionally names the original scaffold identities.
Those mentions are provenance and copy instructions; they are not runtime imports or generated
contract identities in a completed copy.

The concrete example below creates `services/example-copy`. Choose service-specific values before
creating a real service, then use the same replacement categories consistently.

| Identity                       | Scaffold value                                                   | Example copy value                                                |
|--------------------------------|------------------------------------------------------------------|-------------------------------------------------------------------|
| Repository directory           | `templates/go-service`                                           | `services/example-copy`                                           |
| Go module/import prefix        | `github.com/igor-baiborodine/insurance-hub/templates/go-service` | `github.com/igor-baiborodine/insurance-hub/services/example-copy` |
| Runtime `SERVICE_NAME` default | `go-service`                                                     | `example-copy`                                                    |
| Protobuf package               | `scaffold.v1`                                                    | `examplecopy.v1`                                                  |
| Schema/generated path          | `scaffold/v1`                                                    | `examplecopy/v1`                                                  |
| Generated Go package           | `scaffoldv1`                                                     | `examplecopyv1`                                                   |

Use a valid Protobuf identifier without a hyphen. The generic `ExampleService`, `EchoRequest`,
`EchoResponse`, `Echo`, and `cmd/server` names may remain until the service defines a real contract.

## 1. Copy only committed template files

Run these commands from the Insurance Hub repository root. Set `destination` to a path that does not
exist; the guard prevents silently overlaying another service.

```sh
source_revision=HEAD
destination=services/example-copy

test ! -e "$destination"
mkdir -p "$destination"
git archive --format=tar "${source_revision}:templates/go-service" |
  tar -xf - -C "$destination"
cd "$destination"
```

`git archive` copies tracked template content only. It excludes the ignored `.tools/` directory,
local binaries and caches, and repository-local ticket artifacts. The copied `/.tools/` ignore rule
protects the tools installed later.

## 2. Replace every active identity

Delete generated output before changing schema identity. Never edit generated Go files.

```sh
generated_backup=$(mktemp -d)
mv gen "$generated_backup/gen"
printf 'original generated files moved to %s\n' "$generated_backup/gen"
mkdir -p api/examplecopy/v1 testdata/contract-baseline/api/examplecopy/v1
mv api/scaffold/v1/example_service.proto api/examplecopy/v1/example_service.proto
mv \
  testdata/contract-baseline/api/scaffold/v1/example_service.proto \
  testdata/contract-baseline/api/examplecopy/v1/example_service.proto
rmdir \
  api/scaffold/v1 \
  api/scaffold \
  testdata/contract-baseline/api/scaffold/v1 \
  testdata/contract-baseline/api/scaffold
```

Keep that backup outside the module until regeneration and validation succeed, then remove it during
normal temporary-file cleanup. The copy procedure never needs to overwrite generated files in place.

Apply the concrete example replacements below. They cover the Go module and imports, runtime and
test identities, schema and generated paths, the checked-in golangci-lint configuration, tooling
scripts that remain applicable, Make-owned output paths, and the isolated compatibility baseline.
`COPYING.md` is excluded because it must retain the source values needed to create another
independent copy.

```sh
rg -l -0 --hidden --glob '!COPYING.md' --glob '!.tools/**' --glob '!gen/**' \
  'github\.com/igor-baiborodine/insurance-hub/templates/go-service' . |
  xargs -0 -r sed -i \
    's|github\.com/igor-baiborodine/insurance-hub/templates/go-service|github.com/igor-baiborodine/insurance-hub/services/example-copy|g'

rg -l -0 --hidden --glob '!COPYING.md' --glob '!.tools/**' --glob '!gen/**' \
  'templates/go-service' . |
  xargs -0 -r sed -i 's|templates/go-service|services/example-copy|g'

rg -l -0 --hidden --glob '!COPYING.md' --glob '!.tools/**' --glob '!gen/**' \
  'scaffold/v1' . |
  xargs -0 -r sed -i 's|scaffold/v1|examplecopy/v1|g'

rg -l -0 --hidden --glob '!COPYING.md' --glob '!.tools/**' --glob '!gen/**' \
  'scaffold\.v1' . |
  xargs -0 -r sed -i 's|scaffold\.v1|examplecopy.v1|g'

rg -l -0 --hidden --glob '!COPYING.md' --glob '!.tools/**' --glob '!gen/**' \
  'scaffoldv1' . |
  xargs -0 -r sed -i 's|scaffoldv1|examplecopyv1|g'

rg -l -0 --hidden --glob '!COPYING.md' --glob '!.tools/**' --glob '!gen/**' \
  'go-service' . |
  xargs -0 -r sed -i 's|go-service|example-copy|g'

sed -i 's/^# Go service scaffold$/# Example copy service/' README.md
```

The source scaffold's `check-copy` target proves this procedure, but it is not a service-owned
validation capability. Remove that target and script from the copy, and replace the scaffold-only
root-delegate and CI sections with the copied module's actual onboarding boundary:

```sh
sed -i 's/^\([[:space:]]*\)check-copy run$/\1run/' Makefile
sed -i '/^check-copy: verify-tools$/,+1d' Makefile
rm scripts/check-copy.sh

sed -i \
  -e '/^| `make check-copy`/d' \
  -e '/^make check-copy$/d' \
  README.md
awk '
  /^### Root scaffold delegates$/ {
    print "### Repository onboarding"
    print ""
    print "The scaffold-only `go-scaffold-*` root delegates and"
    print "`.github/workflows/go-scaffold.yml` do not cover this copied module. Run module-owned"
    print "Make targets from this directory. Add root and CI coverage only through the repository"
    print "documented in ../../docs/migration/phase-4/go-module-topology.md."
    skipping = 1
    next
  }
  skipping && /^## Create an independently owned service$/ { skipping = 0 }
  !skipping { print }
' README.md > README.md.copy
mv README.md.copy README.md
```

The repository root Makefile and scaffold workflow still point to `templates/go-service`. Do not
claim that they cover the copied service until the repository onboarding workflow updates them.

Review `README.md` and replace the template introduction with the real service purpose when it is
known. Update any service-specific ports or configuration defaults required by the service ticket.
The replacements above update the two Makefile-owned generated paths. The `buf.gen.yaml` output
root remains identity-neutral; do not change it unless the schema layout or tooling contract
changes.

## 3. Bootstrap tools and regenerate owned content

Run setup, dependency maintenance, generation, and formatting only through the copied module's
owning Make targets. These commands may use the network and mutate only their documented paths.

```sh
GOWORK=off make bootstrap-tools
GOWORK=off make update-proto-deps
GOWORK=off make gen-proto
GOWORK=off make update-deps
GOWORK=off make format FORMAT_SCOPE=all
```

Generation must create only:

```text
gen/examplecopy/v1/example_service.pb.go
gen/examplecopy/v1/example_service_grpc.pb.go
```

Commit the regenerated files, changed schema, manifests, lock file, configuration, tests, and
documentation together. Do not add `.tools/`.

## 4. Reject stale active identities

Run this scan from the copied module root. A match outside `COPYING.md` is stale and must be resolved
before validation.

```sh
if rg -n --hidden --glob '!COPYING.md' --glob '!.tools/**' \
  'github\.com/igor-baiborodine/insurance-hub/templates/go-service|templates/go-service|go-service|scaffold\.v1|scaffold_v1|scaffoldv1|scaffold/v1' .
then
  exit 1
fi

test ! -e go.work
if rg -n '^replace([[:space:]]|$)' go.mod
then
  exit 1
fi

test "$(sed -n '1s/^module //p' go.mod)" = \
  'github.com/igor-baiborodine/insurance-hub/services/example-copy'
test -f api/examplecopy/v1/example_service.proto
test -f gen/examplecopy/v1/example_service.pb.go
test -f gen/examplecopy/v1/example_service_grpc.pb.go
```

The generated-identity test inspects the runtime descriptor package, file path, `go_package`,
service name, and full RPC method. Logger and configuration tests capture and assert the renamed
`example-copy` identity. These checks prevent a mutually consistent but stale client and server from
hiding an incomplete rename.

## 5. Validate the independent module without mutation

Capture the module inventory after explicit setup, run the owning Make checks from the copied
module root, and prove those checks did not rewrite it. The aggregate covers tool/configuration
verification, formatting, Protobuf formatting and lint, static analysis, race-enabled tests, build,
dependency drift, generated-output drift and reproducibility, contract compatibility, and reachable
vulnerabilities. Run `test` separately to exercise the non-race test entry point too.

```sh
inventory_before=$(mktemp)
inventory_after=$(mktemp)
trap 'rm -f "$inventory_before" "$inventory_after"' EXIT

find . -type f ! -path './.tools/*' -print0 |
  sort -z | xargs -0 sha256sum >"$inventory_before"

GOWORK=off make test
GOWORK=off make check FORMAT_SCOPE=all

find . -type f ! -path './.tools/*' -print0 |
  sort -z | xargs -0 sha256sum >"$inventory_after"
cmp "$inventory_before" "$inventory_after"
```

The copy remains standalone: it must have no `go.work`, filesystem `replace` directive, import of
the source scaffold, or dependency on its private implementation. This procedure does not add the
copy to the canonical inventory or CI; complete the repository onboarding process before claiming
root aggregate or hosted coverage.

## 6. Run the automated copy proof for scaffold changes

When changing the source scaffold or this procedure, run the owning proof from the original module.
It creates a temporary `services/example-copy`, applies every identity replacement above,
bootstraps that copy's own pinned tools, regenerates its bindings, runs `test` and the complete
non-mutating `check`, verifies that scaffold-only copy tooling and coverage claims were removed,
compares inventories, and removes the temporary tree.

```sh
make check-copy
```

From the repository root, the narrowly scoped equivalent is:

```sh
make go-scaffold-check-copy
```

The topology workflow still covers only modules recorded in `go-module-topology.json`. A copied
service is not covered automatically. Follow the [Go module topology onboarding guide](../../docs/migration/phase-4/go-module-topology.md):
record ownership and resolution mode, add its owning validation target and consumers, update CI
triggers/bootstrap/cache, add positive and negative boundary fixtures, and run both root topology
targets before claiming coverage.
