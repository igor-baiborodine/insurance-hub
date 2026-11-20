<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [MVEL expression library](#mvel-expression-library)
  - [Public API](#public-api)
  - [Grammar and budgets](#grammar-and-budgets)
  - [Owning commands](#owning-commands)
  - [Release](#release)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# MVEL expression library

`github.com/igor-baiborodine/insurance-hub/libs/mvel` compiles and evaluates the restricted
MVEL-compatible expressions used by the accepted Insurance Hub tariff catalog. It returns a Boolean
condition or an exact decimal. It has no dependency on Java, PostgreSQL or Pricing internals.

Callers own rule selection, binding assembly, rounding, aggregation and error mapping. The library
does not cache compiled programs. A cache owned by a caller must key on the original rule text and
the library version.

## Public API

```go
expression, err := mvel.CompileDecimal("AREA * 0.25B")
area, err := mvel.ParseDecimal("0.5")
premium, err := expression.Evaluate(mvel.Bindings{"AREA": mvel.Number(area)})
// premium.String() == "0.125", premium.Scale() == 3
```

- `CompilePredicate` and `CompileDecimal` compile one expression and check its result kind.
- `Predicate` and `DecimalExpression` are immutable and safe for concurrent evaluation with
  independent bindings.
- `Evaluate` requires exactly the identifiers the expression references. A missing, extra or wrongly
  typed binding fails.
- `Decimal` keeps the exact value and represented scale. Its zero value is `0`.
- Errors classify with `errors.Is` as `ErrSyntax`, `ErrUnsupported`, `ErrLimit`, `ErrBinding` or
  `ErrResultKind`. They include a source byte position or binding name, never a binding value.

## Grammar and budgets

The grammar accepts the identifiers `AREA`, `NUM_OF_ADULTS`, `NUM_OF_CHILDREN`, `NUM_OF_CLAIM`
(decimals) and `DESTINATION`, `FLOOD`, `TYP` (strings). It also accepts single-quoted strings,
non-negative decimal literals with an optional `B` suffix, parentheses, decimal `+` and `*`, string
`==`, decimal `>` and `condition ? decimal : decimal`. Every other MVEL feature is rejected as
unsupported, including subtraction, member access, calls and assignment.

| Budget | Limit |
| --- | --- |
| Source | 256 bytes, 128 tokens |
| Structure | 64 AST nodes and evaluation steps, nesting depth 12 |
| Bindings | 7 bindings; names and string values up to 32 bytes |
| Decimals | 38 digits, 20 integer digits, scale 18, applied to literals, bindings and every intermediate result |

Exceeding any budget fails with `ErrLimit`.

## Owning commands

Run commands from the repository root:

```sh
make -C libs/mvel bootstrap-go-tools
make -C libs/mvel check-go-pr FORMAT_SCOPE=all
make -C libs/mvel coverage
```

`check-go-pr` covers tool pins, formatting, lint, race tests, build, dependency consistency,
vulnerabilities and the single-package boundary. The **Go Shared Runtime PR Check** runs it for
MVEL changes and uploads coverage to Codecov under the `mvel` flag. **Go Main Coverage** publishes
the same flag from `main`.

## Release

MVEL releases through the shared
[Go library release workflow](../../docs/go-module-topology.md#shared-library-validation-and-release)
with tags `libs/mvel/vX.Y.Z`. The first release is exactly `libs/mvel/v1.0.0`; later releases
require existing MVEL release history. Public verification resolves the exact version through `proxy.golang.org` and
`sum.golang.org` from an empty-cache standalone consumer. It then evaluates the FAI C1 expression
`10B` and requires the exact decimal `10`.
