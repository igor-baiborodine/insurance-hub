package administration

import _ "embed"

//go:embed queries/acquire-administration-lock.sql
var acquireAdministrationLockQuery string

//go:embed queries/apply-runtime-grants.sql
var applyRuntimeGrantsQuery string

//go:embed queries/inspect-history-rows.sql
var inspectHistoryRowsQuery string

//go:embed queries/inspect-history-schema.sql
var inspectHistorySchemaQuery string

//go:embed queries/inspect-identity-progress.sql
var inspectIdentityProgressQuery string

//go:embed queries/inspect-platform.sql
var inspectPlatformQuery string

//go:embed queries/inspect-policy-schema.sql
var inspectPolicySchemaQuery string

//go:embed queries/inspect-schema-objects.sql
var inspectSchemaObjectsQuery string
