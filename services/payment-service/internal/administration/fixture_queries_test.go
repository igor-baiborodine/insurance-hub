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
