<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Go Service Resource Administration](#go-service-resource-administration)
  - [Ownership and Location](#ownership-and-location)
  - [Standard Interface](#standard-interface)
  - [Naming](#naming)
  - [Operation Pattern](#operation-pattern)
  - [Effective Verification](#effective-verification)
  - [Focused Tests](#focused-tests)
  - [Adoption](#adoption)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Go Service Resource Administration

**Scope: external resources and workload access owned by migrated Go services.** This guide
standardizes the practical patterns established by Dashboard and Policy Search for administering
search indexes, roles and users, Kafka topics, workload Secrets and dependency trust. Keep the
implementation service-local and proportionate to this learning project.

The implemented references are Dashboard's
[reader administration](../../../services/dashboard-service/scripts/reader_administration.py) and
[registration administration](../../../services/dashboard-service/scripts/registration_administration.py),
and Policy Search's
[resource administration](../../../services/policy-search-service/scripts/resource_administration.py).

Resource administration is separate from application startup and ordinary deployment. A service
must not create or repair dependency resources while starting. Database schema migration and seed
data also retain their existing `migrate` and `seed` interfaces; they are not renamed as resource
administration.

## Ownership and Location

Each service owns the contract for the resources it consumes and the identities that may access
them. Put the public targets, scripts, tests and service-specific resource definitions in the
service module:

```text
services/<service>-service/
  Makefile
  scripts/
    administration.py
    resource_administration.py
    test_resource_administration.py
```

Use `resource_administration.py` as the command entry point. Put reusable mechanics for that
service, such as command execution, target selection and Kubernetes helpers, in
`administration.py` only when the entry point would otherwise become difficult to read.

Shared repository tooling may provide generic process and catalog helpers. The shared
`scripts/go/service/Makefile` must contain only targets that work for every Go service. It must not
contain a service name, resource inventory, credential rule or other service-specific behavior.

## Standard Interface

New administration tooling uses these service-local targets:

| Target | Contract |
| --- | --- |
| `resource-inspect` | Read the complete managed boundary with the administrative identity and report compatibility, ownership and drift. Never mutate. |
| `resource-ensure` | Create missing resources and reapply exact access definitions after guarded preflight. Preserve compatible populated resources and existing recoverable credentials. |
| `resource-verify` | Use the workload identities to prove required access and rejection of unrelated or administrative access. Never mutate. |
| `resource-rotate` | Optional explicit credential rotation with recovery or rollback. Do not rotate as a side effect of `ensure`. |
| `resource-revoke` | Optional destructive operation for an explicitly approved retirement scope. Reject active consumers and require a separate exact confirmation. |

The normal baseline is `inspect`, `ensure` and `verify`. Add rotation or revocation only when the
service requirements need them. Do not add a generic `delete`, `reset` or `recreate` operation.

Use this Make pattern:

```make
.PHONY: resource-inspect resource-ensure resource-verify

resource-inspect: ## Inspect owned resources without mutation
resource-ensure: ## Create missing resources without changing compatible state
resource-verify: ## Verify resources and effective workload permissions
resource-inspect resource-ensure resource-verify: python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 KUBECTL="$(KUBECTL)" \
		"$(PYTHON)" scripts/resource_administration.py '$(@:resource-%=%)'
```

Add the scripts to the service's existing Python format, lint and focused test file lists. Keep
the root Make interface generic; root targets may dispatch into this service-local interface
without knowing its resources.

Existing public targets may remain until a scoped ticket changes them. Dashboard's `reader-*` and
`registration-*` targets are historical service-local interfaces; Policy Search's `resource-*`
targets are the preferred shape for new services.

## Naming

Use stable domain and purpose names. Do not put issue numbers, migration stages, `cutover`,
`temporary` or environment names into logical resource identities. Environment belongs in the
deployed Secret name and target catalog.

For a domain named `<service>`:

| Resource | Pattern | Example |
| --- | --- | --- |
| Service directory and logical application | `<service>-service` | `policy-search-service` |
| Kubernetes application label | `<service>-api` | `policy-search-api` |
| Role or user | `go_<service>_<purpose>_<access>` with hyphens changed to underscores | `go_policy_search_catalog_reader` |
| Primary dependency Secret | `<environment>-<service>-<dependency>` | `qa-policy-search-elasticsearch` |
| Additional access Secret | `<environment>-<service>-<dependency>-<access>` | `qa-policy-search-elasticsearch-writer` |
| Trust Secret | `<environment>-<service>-<dependency>-ca` | `qa-policy-search-kafka-ca` |
| Kafka consumer group | `<service>-service` unless the domain contract requires a more specific group | `policy-search-service` |
| Confirmation variable | `<SERVICE>_RESOURCE_CONFIRM` | `POLICY_SEARCH_RESOURCE_CONFIRM` |

Credential Secrets contain only the keys the workload reads, normally `username` and `password`.
Trust Secrets normally contain only `ca.crt`. Label managed Secrets consistently:

```yaml
metadata:
  labels:
    app.kubernetes.io/name: <service>-api
    app.kubernetes.io/component: <dependency>-<reader|writer|trust>
    insurance-hub.io/owner: <service>
```

Use the stable service token for ownership metadata. Ownership records who may administer the
resource; it must not change when the implementation ticket changes.

Keep environment-specific coordinates in the deployment catalog. The service entry identifies
its resources and the selected environment identifies the Kubernetes context, service namespace,
data namespace, administrative Secret, workload Secrets, trust Secrets and dependency endpoint.
Scripts must consume these coordinates rather than deriving or searching for a target.

## Operation Pattern

Use one small dispatcher with explicit operations:

```python
OPERATIONS = {"inspect", "ensure", "verify"}
MUTATING_OPERATIONS = {"ensure"}


def execute(operation: str) -> None:
    if operation not in OPERATIONS:
        raise AdministrationError("unsupported resource operation")
    if operation in MUTATING_OPERATIONS:
        require_exact_confirmation(operation)

    target = load_exact_target()
    target.verify_coordinates()
    current = inspect_managed_boundary(target)

    if operation == "inspect":
        report(current)
    elif operation == "ensure":
        reject_incompatible_or_foreign_state(current)
        ensure_missing_resources(target, current)
        verify_managed_boundary(target)
    else:
        verify_managed_boundary(target)
```

For a mutation, require an exact value such as
`ensure-<environment>-<service>-resources` in `<SERVICE>_RESOURCE_CONFIRM`. Check it before any
cluster or dependency contact. Rotation and revocation use their own operation name in the value.

Apply these rules in order:

1. Validate the operation and exact confirmation for a mutation.
2. Load one explicit environment and verify its configured context and namespaces.
3. Inspect the complete managed boundary before the first mutation.
4. Reject incompatible definitions, foreign ownership and ambiguous partial state.
5. Preserve compatible populated resources and existing recoverable credentials.
6. Create only missing state or reapply the exact owned role and access definition.
7. Verify through the workload identity and report a redacted result.

When creation of a remote identity could leave an unrecoverable password after interruption,
persist the generated credential in its workload Secret before enabling that identity. For an
update, retain the previous credential until the new credential and Secret have both been verified;
restore it if the update fails. Never print credentials or pass them in command arguments.

Send Secret manifests and sensitive API bodies through standard input. Use argument arrays rather
than shell command strings, a minimal environment, command deadlines, bounded output and reliable
cleanup for port forwards and child process groups. Error messages may name the failed resource but
must not include Secret values, authorization headers or sensitive response bodies.

`ensure` must not delete or reset indexes, tables, topics, documents or messages. It must not
replace compatible mappings or rotate an existing usable credential. If existing state cannot be
proved compatible and owned, stop before mutation and report the mismatch.

## Effective Verification

Verification proves behavior, not only stored configuration:

- Check the exact index mapping, topic settings or other service-owned resource definition.
- Compare trust material byte-for-byte with the selected environment's public CA source.
- Authenticate as each workload identity using the credentials from its workload Secret.
- Prove every required read or write action against the owned resource.
- Prove rejection of unrelated resources and administrative actions.
- Check the exact Secret keys, stable labels and expected namespace.
- Leave resources, credentials and data unchanged.

Administrative inspection may use a privileged identity. Effective verification must use the same
identity and Secret that the workload uses so a correct-looking role definition cannot hide a bad
binding or credential.

## Focused Tests

Keep the test set small and centered on failure boundaries:

- Reject wrong environment, context or namespace coordinates before target access.
- Require exact mutation confirmation before target access.
- Create an absent resource set and preserve compatible populated state.
- Reject incompatible or foreign state before the first mutation.
- Recover safely from a credential Secret or remote identity missing on only one side.
- Apply exact Secret keys, labels and trust data without exposing credentials.
- Prove required permissions and rejection of unrelated and administrative access.
- Clean up child processes and redact bounded command failures.
- If rotation exists, prove successful replacement and rollback after a failed update.

Use fakes for focused administration tests. Add a disposable dependency or cluster smoke only when
the underlying resource behavior cannot be established by the focused tests. Ordinary application
tests do not need to exercise privileged administration.

## Adoption

Use this interface for new services and new administration boundaries. When touching an existing
service, preserve its public targets unless the ticket explicitly includes alignment. Consolidating
Dashboard's existing reader and registration entry points can therefore happen independently of
using the same naming and safety rules for future work.

Dashboard and Policy Search remain implementation examples, not libraries to import. Copy the
small pattern that applies to the new resource boundary and keep the inventory, compatibility
rules and tests owned by the consuming service.
