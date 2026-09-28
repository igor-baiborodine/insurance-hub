# Go Validation

Use only the corresponding Makefile targets, as required by
[Go development](go-development.md#scope-and-makefile-interface). Never invoke underlying test,
lint, build, generation, dependency, or security tools directly to bypass a missing/failing target.

## Discover the validation boundary

- Read module/workspace manifests, Makefiles (including prerequisites), relevant CI, and service
  instructions. Identify changed modules, generated contracts, and dependent consumers.
- Inspect each target's actual coverage: package selection, nested modules, build tags, race
  instrumentation, integration dependencies, and whether it changes files. A root target or a
  workspace does not by itself prove that every service is tested.
- Run the narrowest meaningful targets during implementation, then all applicable module/CI gates.
  Use supported Make variables for package/test selection; do not invent variables or direct CLI
  alternatives. Include changed packages outside a target's default selection.
- Do not run broad aggregates blindly. In the example, `check` tidies dependencies, formats files,
  and runs both unit and integration tests. Dependency maintenance is appropriate only when imports
  or dependencies change, and integration prerequisites must be available.

## Required checks

The names below are a target contract for Phase 4 service scaffolding. Examples from
`campsite-booking-go` are marked explicitly; none is a claim that Insurance Hub already implements
the target. Use documented equivalents in the owning Makefile and its supported working directory
(for example, `make -C <service-directory> lint` only when that Makefile supports this invocation).

| Capability / suggested target | When and what to verify |
| --- | --- |
| `format`, `format-check` | For changed handwritten Go files: apply canonical formatting and run the non-mutating check. The example has `format`; its `check-format-diff` is only a Git drift check. |
| `lint` | For affected Go modules: static analysis, including vet-equivalent checks, using pinned configuration. The example uses golangci-lint. |
| `test` | Test changed behavior and all packages in affected modules before completion, plus affected consumers. The example enables the race detector but selects only `internal/...`; that does not cover `cmd/`, other packages, or its nested data-generator module. |
| `build` | Compile affected executables and contract consumers; test success alone does not prove deployable entry points build. |
| `test-integration` | When changing adapters, persistence, migrations, transport wiring, or cross-service behavior, and whenever CI requires it. The example uses the `integration` build tag and Testcontainers. |
| `format-proto`, `lint-proto`, `gen-proto` | For changed protobuf contracts: format/lint definitions and regenerate all configured output. These targets exist in the example. |
| `check-proto-breaking` | For changed published contracts: check compatibility against the documented released/base contract, including wire and HTTP behavior tests. Buf breaking configuration alone does not execute this check; the example has no such Make target. |
| `gen-mock`, generation drift checks | When interfaces or generators change: regenerate affected mocks/clients and verify reproducibility. The example has `gen-mock`, `check-proto-diff`, and `check-mock-diff`; inspect their scope before reuse. |
| `mod-tidy`, `check-mod-diff` | Only for affected imports/dependencies or a required CI dependency gate. Review both manifest and checksum changes. These names exist in the example; its diff checks assume a clean baseline. |
| `test-race` or race-enabled `test` | Required for concurrency, synchronization, caches, shared state, workers, and race-enabled CI. Do not repeat a check already covered by an equivalent race-enabled target. |
| `vulncheck` | Reachability-aware vulnerability checks when dependencies or security-sensitive code change, or CI requires them. Supply a Make target for the pinned analyzer; the example has no such target. |

- A missing required capability is a tooling gap: add the target if in scope, otherwise record the
  check as blocked with the exact missing target/prerequisite. Do not downgrade it to optional or
  claim success from inspection. Check tool/runtime availability through repository targets.
- Keep aggregate validation targets predictable. A check target must not silently install tools,
  change dependencies, apply database migrations to shared environments, or deploy services.
  Tool installation and environment setup belong in explicit Make targets.
- Make generation checks compare against a suitable baseline and detect missing/new generated
  files as well as modified tracked output. Never force a clean worktree to satisfy a CI-oriented
  diff target. When changing generators or formatting, verify reproducibility/idempotence through
  the owning targets.

## Migration acceptance

- For Phase 4 rewrites, verify the legacy parity cases recorded in the ticket: success and failure
  responses, validation, permissions, events, persistence, and serialization semantics.
- Test real migrations and transaction/conflict behavior with disposable dependencies. Unit mocks
  cannot prove database constraints, isolation, or compatibility with the coexisting Java service.
- Before traffic cutover, run the ticket's integration, end-to-end, and performance Make targets
  against the Go service's internal endpoints and compare with the Java baseline. Record rollback
  readiness and rollout criteria; a unit-test pass alone does not authorize traffic changes.
- Verify startup/configuration failures, health probes, cancellation and graceful shutdown, and
  telemetry delivery/correlation through Alloy. Report local-only observations separately from
  cluster or collector verification; do not infer delivery merely from instrumentation code.

## Evidence and current availability

- Record exact Make invocations, working directories, relevant variables/module coverage, and
  results in ticket artifacts. Distinguish passed, failed, blocked, and not applicable checks.
- Report pre-existing failures separately from regressions. Preserve useful failure evidence;
  never hide failures, weaken gates, or state that unexecuted checks passed.
- After checks, inspect tracked and relevant new files for unintended generated, formatting, or
  dependency changes. Apply the repository post-step workflow and before-merge checklist.
- Insurance Hub currently has no Go module/workspace manifests. Its root `go-build` recipe is a
  placeholder, not a multi-service validation suite. Phase 4 scaffolding must establish module
  ownership and the necessary Make targets, pinned tools, exclusions, and CI invocation before
  service validation is executable. Documentation-only changes to these rules need reference,
  consistency, and whitespace review; they do not establish runtime or toolchain correctness.
