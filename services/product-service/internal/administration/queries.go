package administration

import _ "embed"

//go:embed queries/inspect-product-schema.sql
var inspectProductSchemaQuery string

//go:embed queries/inspect-schema-objects.sql
var inspectSchemaObjectsQuery string

//go:embed queries/inspect-platform.sql
var inspectPlatformQuery string

//go:embed queries/inspect-history-schema.sql
var inspectHistorySchemaQuery string

//go:embed queries/inspect-history-rows.sql
var inspectHistoryRowsQuery string
