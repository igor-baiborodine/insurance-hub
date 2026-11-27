package administration

import _ "embed"

//go:embed queries/acquire-administration-lock.sql
var acquireAdministrationLockQuery string

//go:embed queries/insert-payment-schema-version.sql
var recordSchemaVersionQuery string

//go:embed queries/inspect-history-rows.sql
var inspectHistoryRowsQuery string

//go:embed queries/inspect-history-schema.sql
var inspectHistorySchemaQuery string

//go:embed queries/inspect-platform.sql
var inspectPlatformQuery string

//go:embed queries/inspect-payment-schema.sql
var inspectPaymentSchemaQuery string

//go:embed queries/inspect-schema-objects.sql
var inspectSchemaObjectsQuery string
