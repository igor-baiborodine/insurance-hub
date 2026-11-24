// Package provisioning holds the platform access SQL that resource administration runs against
// CNPG and the disposable suites run against PostgreSQL containers.
package provisioning

import _ "embed"

// ProvisionAccess creates the enrollment marker and database, schema and membership grants.
//
//go:embed provision-access.sql
var ProvisionAccess string

// InspectAccess returns true only when both Policy logins have exactly the contracted access.
//
//go:embed inspect-access.sql
var InspectAccess string
