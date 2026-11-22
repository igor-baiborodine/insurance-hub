//go:build integration

package administration_test

import _ "embed"

//go:embed testdata/sql/setup-migration-target-identity.sql
var setupMigrationFixtureQuery string

//go:embed testdata/sql/snapshot-pg-class.sql
var snapshotCatalogQuery string

//go:embed testdata/sql/revoke-migrator.sql
var revokeMigratorQuery string

//go:embed testdata/sql/grant-migrator.sql
var grantMigratorQuery string

//go:embed testdata/sql/read-pricing-schema-version-timestamp.sql
var readMigrationTimestampQuery string

//go:embed testdata/sql/truncate-tariff-tables.sql
var truncateTariffsQuery string

//go:embed testdata/sql/update-base-price-rules-car-formula.sql
var changeCarFormulaQuery string

//go:embed testdata/sql/duplicate-base-price-rules-car-rule.sql
var duplicateCarRuleQuery string

//go:embed testdata/sql/update-base-price-rules-car-position.sql
var contradictCarPositionQuery string

//go:embed testdata/sql/update-discount-markup-rule-hsi-factor.sql
var changeHSIMarkupFactorQuery string

//go:embed testdata/sql/count-tariff.sql
var countTariffsQuery string

//go:embed testdata/sql/read-ordered-tariff.sql
var readOrderedTariffQuery string

//go:embed testdata/sql/snapshot-tariff-seed-state.sql
var snapshotSeedStateQuery string

//go:embed testdata/sql/update-target-identity-seed-policy.sql
var setSeedMarkerQuery string

//go:embed testdata/sql/drop-pricing-schema-version-timestamp-required.sql
var dropHistoryTimestampRequiredQuery string

//go:embed testdata/sql/restore-pricing-schema-version-timestamp-required.sql
var restoreHistoryTimestampRequiredQuery string

//go:embed testdata/sql/read-tariff-codes.sql
var readTariffCodesQuery string

//go:embed testdata/sql/deny-tariff-update.sql
var denyTariffUpdateQuery string

//go:embed testdata/sql/deny-tariff-alter.sql
var denyTariffAlterQuery string

//go:embed testdata/sql/setup-reader-scenarios.sql
var setupReaderScenariosQuery string

//go:embed testdata/sql/delete-reader-scenarios.sql
var deleteReaderScenariosQuery string

//go:embed testdata/sql/setup-prior-managed-tariffs.sql
var setupPriorSchemaQuery string

//go:embed testdata/sql/verify-prior-id-inserts.sql
var verifyPriorInsertQuery string

//go:embed testdata/sql/inspect-identity-catalog.sql
var inspectIdentityCatalogQuery string

//go:embed testdata/sql/setup-unmanaged-tariff.sql
var setupUnmanagedTariffQuery string

//go:embed testdata/sql/drop-unmanaged-tariff.sql
var dropUnmanagedTariffQuery string
