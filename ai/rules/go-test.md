<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Go Testing](#go-testing)
  - [Files and discovery](#files-and-discovery)
  - [Test function names](#test-function-names)
  - [Subtests and table cases](#subtests-and-table-cases)
  - [Behavior, structure, and assertions](#behavior-structure-and-assertions)
  - [Isolation, helpers, and integration tests](#isolation-helpers-and-integration-tests)
  - [Related test functions](#related-test-functions)
  - [Adoption and review](#adoption-and-review)
  - [Sources](#sources)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Go Testing

Apply to handwritten Go tests and test helpers, including Product Service and the service scaffold.
Use [Go development](go-development.md#scope-and-makefile-interface) for the mandatory Makefile
interface, [Go formatting](go-formatting.md) for formatting, and
[Go validation](go-validation.md) for execution scope and evidence. Adapt the
[testing examples](../examples/go-testing/README.md) to actual module APIs and ticket requirements.

## Files and discovery

- Put tests in `*_test.go`, usually beside the code they exercise with a matching subject filename.
  Do not prefix runnable test files with `_` or `.`; Go ignores those files. Platform suffixes
  before `_test.go` still constrain which systems run them.
- Use `func TestXxx(t *testing.T)` with no return values. Go requires that the suffix after `Test`
  not start with a lowercase letter; use a meaningful subject starting with an uppercase letter
  in this repository. A name such as `Testparse` is not discovered as a test.
- Use an external `package foo_test` for public behavior when practical; use `package foo` when
  internal behavior is the intended boundary. Do not export production internals solely for tests.
- Keep unit tests untagged unless the module requires otherwise. Follow the module's `integration`
  build tag and owning Make target for integration tests; `_integration_test.go` alone does not
  exclude a file from ordinary test runs. Store package-local fixtures under `testdata/` when useful.

## Test function names

The underscore separators below are mandatory repository readability conventions for new or
renamed tests, not Go toolchain requirements. Keep MixedCaps within each component; separate the
subject, method (when named), and scenario or behavior with `_`.

| Test purpose | Pattern | Example |
| --- | --- | --- |
| Function or coherent suite | `TestSubject` | `TestValidatePeriod` |
| Method | `TestType_Method` | `TestPolicyServer_GetPolicy` |
| Specific scenario or behavior | `TestSubject_ScenarioOrBehavior` | `TestLoad_ShutdownTimeout` |
| Method with a specific scenario | `TestType_Method_ScenarioOrBehavior` | `TestPolicyServer_GetPolicy_NotFound` |

- Name the actual subject and distinguishing condition or observable behavior. Avoid generic
  labels such as `TestSuccess`, `TestError`, numbered cases, and long unbroken subject/behavior
  phrases. Do not put an underscore between every word or add redundant `Should`/`Test` wording.
- Preserve recognizable API names and initialisms such as `HTTP`, `ID`, and `JSON`. A suite may
  describe a business boundary without corresponding to exactly one production function.
- Use a short parent test with named subtests for related cases sharing the same operation and
  assertion shape. Keep scenarios with substantially different setup or assertions separate;
  do not force every test into a table or invent extra subtests solely to shorten a name.

Examples of applying the convention to existing Product Service names (illustrative renames):

| Existing name | Name following this rule |
| --- | --- |
| `TestDecimalPreservesPresencePrecisionAndScale` | `TestDecimal_PreservesPresencePrecisionAndScale` |
| `TestQuestionRequiresOneKnownValueVariant` | `TestQuestion_RequiresOneKnownValueVariant` |
| `TestProductServiceReturnsEmptyList` | `TestProductService_ReturnsEmptyList` |
| `TestProductLifecycle` | `TestProductLifecycle` (a short suite name is already suitable) |

## Subtests and table cases

- Give table cases a `name` field and run them with `t.Run`, or use unique descriptive map keys.
  Prefer short lowercase phrases such as `"empty catalog"` or `"cancelled context"`. Include the
  expected behavior when it clarifies the case, for example `"missing code returns not found"`.
- Names must be nonempty, stable, and unique among siblings after Go's whitespace normalization.
  Spaces appear as underscores in output: `TestListProducts/empty_catalog`. Avoid relying on Go's
  automatic duplicate suffixes, case numbers, timestamps, pointers, or dumps of complex inputs.
- Preserve authoritative baseline scenario IDs used for parity/coverage traceability; these are
  legitimate case names. Do not rewrite fixture IDs merely to make them lowercase phrases.
- Avoid `/` unless intentionally creating a filtering hierarchy; avoid unnecessary regular
  expression metacharacters. Test selection matches slash-separated name components. Pass any
  selection through supported Make variables/targets, never a direct test-tool invocation.
- Prefer slices for a predictable declared case order. Map iteration order is unspecified and
  must not affect results. Allocate mutable fixtures and mocks per case; cases must not depend
  on siblings having run first.
- Prefer `got`, `want`, and `wantErr` where they improve clarity; descriptive domain-specific
  names and established module conventions are also valid. Do not introduce nested `args` or
  `fields` wrappers unless they simplify the table.

## Behavior, structure, and assertions

- Test changed observable behavior, failures, and boundaries. Use table-driven cases when helpful
  and the module's established assertion conventions. Structure each applicable scenario with the
  exact lowercase comments `// given`, `// when`, and `// then`: put scenario setup, test data, and
  mock expectations under `given`; the behavior-triggering operation under `when`; and observations
  plus mock/call verification under `then`. Shared immutable fixtures for table-driven tests may use
  an outer `given`; keep per-case setup and the `when`/`then` phases in the subtest. Omit the markers
  from shared setup helpers and trivial tests where a phase would be empty or misleading; do not add
  placeholder blocks.
- Follow the example's unit-test separation, generated Mockery/Testify mocks, and tagged
  integration tests where those tools are adopted. Mock dependency boundaries, not implementation
  details; regenerate mocks through Make when interfaces change.
- Derive expected results independently of the implementation under test. Do not compute expected
  responses with the same production mapper being tested or weaken assertions to accommodate a
  regression. Match wrapped errors with `errors.Is`/`errors.As`; compare exact error text only
  when the text itself is part of the contract.
- Fail immediately on prerequisites needed for safe continuation (`t.Fatal`/`require`); use
  nonfatal assertions for independent observations (`t.Error`/`assert`). Failure messages should
  identify the operation/input and observed versus expected values; useful diffs are preferable
  to a generic "failed". Keep standard-library assertions where the module already uses them.
- Include contract/status mapping, cancellation, rollback/conflicts, and legacy parity cases as
  appropriate. Test names must describe what assertions actually prove.

## Isolation, helpers, and integration tests

- Keep tests deterministic: control time and test data, use synchronization rather than sleeps,
  register cleanup, and call `t.Helper()` in helpers that accept a testing handle. Use descriptive
  lowerCamelCase names for package-local helpers, such as `newTestServer`; reserve `Test...` for
  discovered tests. Exported shared test helpers follow ordinary Go export naming.
- Use `t.TempDir`, `t.Setenv`, and `t.Cleanup` where appropriate. Register resource cleanup as soon
  as acquisition succeeds, including partial setup paths. Cleanup for resources shared with
  subtests must outlive those subtests; prefer `t.Cleanup` over parent `defer` for that lifetime.
- Call `t.Parallel()` only when fixtures, mocks, storage, and process state are isolated. Tests
  using `t.Setenv` or `t.Chdir` cannot be parallel or have parallel ancestors. Per-iteration loop
  variables in modern Go do not isolate shared pointers, maps, or external resources.
- Bound blocking work with deadlines and provide goroutines with cancellation and a join path.
  Send worker failures to the test goroutine; do not call `t.Fatal`, `FailNow`, or fatal `require`
  assertions from workers. Timeouts bound failures; channels or other synchronization order work.
- Use isolated disposable dependencies (such as PostgreSQL Testcontainers) for integration tests,
  with the actual migrations and reliable cleanup. Never point tests at shared production data.
  Unit mocks cannot prove database constraints, transaction isolation, or real transport wiring.
  Report missing integration prerequisites; do not silently skip a required gate.

## Related test functions

- Use `BenchmarkXxx(b *testing.B)` and `FuzzXxx(f *testing.F)` for benchmarks and fuzz targets;
  apply the same readable subject/scenario separation. Keep setup outside measured benchmark
  work and fuzz inputs independent and reproducible. Execute through owning Make targets.
- Executable examples have their own naming grammar: `Example`, `ExampleFunc`, `ExampleType`,
  `ExampleType_Method`, optionally followed by a suffix starting with a lowercase letter, such
  as `ExampleType_Method_empty`. Do not impose uppercase test-scenario suffixes on examples.
- Reserve `TestMain(m *testing.M)` for necessary package-wide lifecycle work; prefer per-test
  setup and cleanup when sufficient.

## Adoption and review

- Apply these rules to new tests and tests whose behavior or structure is edited. Update affected
  names as part of that work; keep broad renaming of untouched tests in a dedicated change.
- Before renaming, inspect Make/CI filters, baseline registrations, documentation, and any use of
  `t.Name()` for fixture paths. Preserve or update those references together, including tagged
  integration suites. Renaming must not silently drop selected tests or break fixture lookup.
- Review naming and test intent against this rule. Go discovery/vet checks do not enforce the
  repository's underscore convention, and no automatic naming lint gate is introduced here.
  Follow [Go validation](go-validation.md) for applicable Make checks and evidence after test edits.

## Sources

- [Go testing package](https://pkg.go.dev/testing): discovery, examples, lifecycle, and isolation APIs.
- [Go command test documentation](https://pkg.go.dev/cmd/go#hdr-Test_packages): file/package discovery.
- [Go subtests guide](https://go.dev/blog/subtests): naming, selection, and parallel subtest lifetimes.
- [Effective Go naming](https://go.dev/doc/effective_go#mixed-caps): MixedCaps within identifiers;
  underscore-separated test components above are this repository's deliberate convention.
