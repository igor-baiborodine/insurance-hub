// Package postgres owns Policy PostgreSQL connections.
package postgres

import _ "embed"

//go:embed queries/assume-owner.sql
var assumeOwnerQuery string

//go:embed queries/administration-settings.sql
var administrationSettingsQuery string

//go:embed queries/inspect-writer-readiness.sql
var inspectWriterReadinessQuery string
