//go:build integration

package administration_test

import _ "embed"

//go:embed testdata/sql/setup-unrelated-service.sql
var setupUnrelatedServiceQuery string

//go:embed testdata/sql/close-unrelated-service.sql
var closeUnrelatedServiceQuery string

//go:embed testdata/sql/grant-writer-delete.sql
var grantWriterDeleteQuery string

//go:embed testdata/sql/revoke-writer-delete.sql
var revokeWriterDeleteQuery string

//go:embed testdata/sql/grant-writer-owner.sql
var grantWriterOwnerQuery string

//go:embed testdata/sql/revoke-writer-owner.sql
var revokeWriterOwnerQuery string

//go:embed testdata/sql/insert-writer-outbox-intent.sql
var insertWriterOutboxIntentQuery string

//go:embed testdata/sql/update-writer-offer.sql
var updateWriterOfferQuery string

//go:embed testdata/sql/read-catalog-snapshot.sql
var readCatalogSnapshotQuery string

//go:embed testdata/sql/read-migration-history.sql
var readMigrationHistoryQuery string

//go:embed testdata/sql/read-allocator-catalog.sql
var readAllocatorCatalogQuery string

//go:embed testdata/sql/setup-partial-schema.sql
var setupPartialSchemaQuery string

//go:embed testdata/sql/drop-partial-schema.sql
var dropPartialSchemaQuery string

//go:embed testdata/sql/insert-unknown-history.sql
var insertUnknownHistoryQuery string

//go:embed testdata/sql/delete-unknown-history.sql
var deleteUnknownHistoryQuery string

//go:embed testdata/sql/rewind-offer-allocator.sql
var rewindOfferAllocatorQuery string

//go:embed testdata/sql/restore-offer-allocator.sql
var restoreOfferAllocatorQuery string

//go:embed testdata/sql/change-cover-foreign-key-action.sql
var changeCoverForeignKeyActionQuery string
