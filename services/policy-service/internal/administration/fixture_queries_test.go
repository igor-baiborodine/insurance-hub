//go:build integration

package administration_test

import _ "embed"

//go:embed testdata/sql/setup-target-identity.sql
var setupTargetIdentityQuery string

//go:embed testdata/sql/setup-java-fixture.sql
var setupJavaFixtureQuery string

//go:embed testdata/sql/read-business-facts.sql
var readBusinessFactsQuery string

//go:embed testdata/sql/read-row-fingerprints.sql
var readRowFingerprintsQuery string

//go:embed testdata/sql/insert-generated-aggregate.sql
var insertGeneratedAggregateQuery string

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
