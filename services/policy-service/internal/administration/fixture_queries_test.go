//go:build integration

package administration_test

import _ "embed"

//go:embed provisioning/provision-access.sql
var provisionAccessQuery string

//go:embed provisioning/inspect-access.sql
var inspectAccessQuery string

//go:embed testdata/sql/setup-unrelated-service.sql
var setupUnrelatedServiceQuery string

//go:embed testdata/sql/close-unrelated-service.sql
var closeUnrelatedServiceQuery string

//go:embed testdata/sql/grant-runtime-delete.sql
var grantRuntimeDeleteQuery string

//go:embed testdata/sql/revoke-runtime-delete.sql
var revokeRuntimeDeleteQuery string

//go:embed testdata/sql/grant-runtime-owner.sql
var grantRuntimeOwnerQuery string

//go:embed testdata/sql/revoke-runtime-owner.sql
var revokeRuntimeOwnerQuery string

//go:embed testdata/sql/insert-runtime-outbox-intent.sql
var insertRuntimeOutboxIntentQuery string

//go:embed testdata/sql/update-runtime-offer.sql
var updateRuntimeOfferQuery string

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
