<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Java Rules](#java-rules)
  - [Tests](#tests)
    - [Structure](#structure)
    - [Assertions](#assertions)
    - [Fixtures](#fixtures)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Java Rules

These rules are intentionally minimal until service-specific Java conventions are formalized.

Apply [Naming rules](naming-rules.md) when creating or renaming Java source files, package
directories, or surrounding repository paths.

- Follow the existing style of the target module.
- Keep API contracts and service implementation boundaries intact.
- Prefer constructor injection and established Micronaut patterns already present in the module.
- Keep DTO validation explicit and close to the contract boundary.
- Add or update tests near the changed production code when behavior changes.
- Do not introduce broad framework upgrades or dependency changes as part of feature work unless the ticket requires them.

## Tests 

### Structure

When writing or updating JUnit 5 unit or integration tests, prefer an explicit given-when-then
structure over helper-driven BDD wrappers. Apply the repository-wide
[testability assessment](repo-rules.md#testability) when fixture wiring or test implementation
becomes complicated.

- Name classes with unit tests following `<ClassName>Test` pattern.
- Name classes with integration tests following `<ClassName>IT` pattern.
- Group tests for a specific production method inside a JUnit 5 `@Nested` class.
- Name the instance under test `classUnderTest`.
- Use `happyPath` for the main successful case.
- Use `given<preconditions_and_inputs>_then<expected_results>` for other scenarios.
- Structure each test method with explicit `// given`, `// when`, and `// then` comments.
- Keep the `when` block limited to the method invocation under test.
- Store the invocation result in a variable named `result`.

### Assertions

- Prefer AssertJ assertions in Java tests for readable, fluent assertion chains.

### Fixtures

- Create test fixtures with Easy Random when randomized object graphs or broad sample data reduce
  test setup noise.
- Prefer Easy Random over ad hoc object builders for repeated fixture creation in Java tests.
