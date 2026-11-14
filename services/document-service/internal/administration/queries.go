package administration

import _ "embed"

//go:embed queries/inspect-document-schema.sql
var inspectDocumentSchemaQuery string

//go:embed queries/inspect-schema-objects.sql
var inspectSchemaObjectsQuery string

//go:embed queries/inspect-platform.sql
var inspectPlatformQuery string

//go:embed queries/inspect-history-schema.sql
var inspectHistorySchemaQuery string

//go:embed queries/inspect-history-rows.sql
var inspectHistoryRowsQuery string

//go:embed queries/inspect-sequence.sql
var inspectSequenceQuery string

//go:embed queries/inspect-sequence-progress.sql
var inspectSequenceProgressQuery string

//go:embed queries/acquire-administration-lock.sql
var acquireAdministrationLockQuery string

//go:embed queries/apply-reader-grants.sql
var applyReaderGrantsQuery string

//go:embed queries/apply-writer-grants.sql
var applyWriterGrantsQuery string

//go:embed queries/inspect-document-presence.sql
var inspectDocumentPresenceQuery string

//go:embed queries/lock-document.sql
var lockDocumentQuery string
