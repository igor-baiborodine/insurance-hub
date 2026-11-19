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

//go:embed testdata/sql/setup-incompatible-tariff-schema.sql
var setupIncompatibleSchemaQuery string

//go:embed testdata/sql/verify-pricing-schema-version-rollback.sql
var verifyMigrationRollbackQuery string

//go:embed testdata/sql/remove-discount-markup-rule-conflicting-column.sql
var removeConflictingColumnQuery string

//go:embed testdata/sql/assume-pricing-owner.sql
var assumePricingOwnerQuery string

//go:embed testdata/sql/read-legacy-tariff.sql
var readLegacyTariffQuery string

//go:embed testdata/sql/insert-unpositioned-base-price-rules.sql
var insertUnpositionedRuleQuery string

//go:embed testdata/sql/reset-role.sql
var resetRoleQuery string

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

//go:embed testdata/sql/insert-legacy-tariff.sql
var insertLegacyTariffQuery string

//go:embed testdata/sql/insert-legacy-base-price-rules.sql
var insertLegacyBaseRuleQuery string

//go:embed testdata/sql/insert-legacy-discount-markup-rule.sql
var insertLegacyMarkupRuleQuery string

//go:embed testdata/sql/count-tariff.sql
var countTariffsQuery string

//go:embed testdata/sql/read-ordered-tariff.sql
var readOrderedTariffQuery string

//go:embed testdata/sql/snapshot-legacy-tariff-identity.sql
var snapshotLegacyIdentityQuery string

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
