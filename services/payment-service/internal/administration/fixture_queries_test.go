//go:build integration

package administration_test

import _ "embed"

//go:embed testdata/sql/read-catalog-snapshot.sql
var readCatalogSnapshotQuery string

//go:embed testdata/sql/read-migration-history.sql
var readMigrationHistoryQuery string

//go:embed testdata/sql/read-allocator-catalog.sql
var readAllocatorCatalogQuery string

//go:embed testdata/sql/read-business-counts.sql
var readBusinessCountsQuery string

//go:embed testdata/sql/setup-partial-schema.sql
var setupPartialSchemaQuery string

//go:embed testdata/sql/drop-partial-schema.sql
var dropPartialSchemaQuery string

//go:embed testdata/sql/insert-unknown-history.sql
var insertUnknownHistoryQuery string

//go:embed testdata/sql/delete-unknown-history.sql
var deleteUnknownHistoryQuery string

//go:embed testdata/sql/change-entry-foreign-key-action.sql
var changeEntryForeignKeyActionQuery string

//go:embed testdata/sql/restore-entry-foreign-key-action.sql
var restoreEntryForeignKeyActionQuery string

//go:embed testdata/sql/change-receipt-state-check.sql
var changeReceiptStateCheckQuery string

//go:embed testdata/sql/restore-receipt-state-check.sql
var restoreReceiptStateCheckQuery string

//go:embed testdata/sql/insert-writer-registration.sql
var insertWriterRegistrationQuery string

//go:embed testdata/sql/insert-writer-account-without-premium.sql
var insertWriterAccountWithoutPremiumQuery string

//go:embed testdata/sql/insert-writer-account-without-cover.sql
var insertWriterAccountWithoutCoverQuery string

//go:embed testdata/sql/read-writer-registration-match.sql
var readWriterRegistrationMatchQuery string

//go:embed testdata/sql/insert-writer-entry.sql
var insertWriterEntryQuery string

//go:embed testdata/sql/insert-writer-receipt.sql
var insertWriterReceiptQuery string

//go:embed testdata/sql/update-writer-receipt-state.sql
var updateWriterReceiptStateQuery string

//go:embed testdata/sql/read-writer-receipt-facts.sql
var readWriterReceiptFactsQuery string

//go:embed testdata/sql/read-writer-balance.sql
var readWriterBalanceQuery string

//go:embed testdata/sql/grant-writer-delete.sql
var grantWriterDeleteQuery string

//go:embed testdata/sql/revoke-writer-delete.sql
var revokeWriterDeleteQuery string

//go:embed testdata/sql/grant-writer-receipt-update.sql
var grantWriterReceiptUpdateQuery string

//go:embed testdata/sql/grant-writer-entry-amount-update.sql
var grantWriterEntryAmountUpdateQuery string

//go:embed testdata/sql/revoke-writer-entry-amount-update.sql
var revokeWriterEntryAmountUpdateQuery string
