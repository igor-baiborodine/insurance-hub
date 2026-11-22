package administration

import _ "embed"

//go:embed queries/acquire-administration-lock.sql
var acquireAdministrationLockQuery string

//go:embed queries/insert-pricing-schema-version.sql
var recordMigrationVersionQuery string

//go:embed queries/inspect-platform-marker.sql
var inspectPlatformMarkerQuery string

//go:embed queries/inspect-history-rows.sql
var inspectHistoryRowsQuery string

//go:embed queries/inspect-pricing-schema.sql
var inspectPricingSchemaQuery string

//go:embed queries/lock-tariff-tables.sql
var lockTariffsQuery string

//go:embed queries/find-tariff.sql
var findTariffQuery string

//go:embed queries/read-base-price-rules.sql
var readBaseRulesQuery string

//go:embed queries/read-discount-markup-rule.sql
var readMarkupRulesQuery string

//go:embed queries/insert-tariff.sql
var insertTariffQuery string

//go:embed queries/inspect-platform.sql
var inspectPlatformQuery string

//go:embed queries/inspect-schema-objects.sql
var inspectSchemaObjectsQuery string

//go:embed queries/inspect-history-schema.sql
var inspectHistorySchemaQuery string

//go:embed queries/grant-tariff-access.sql
var grantTariffAccessQuery string
