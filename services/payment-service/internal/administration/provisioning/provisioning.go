// Package provisioning holds the platform access SQL that the disposable suites run against
// PostgreSQL containers, matching the access an enrolled Payment CNPG target must have.
package provisioning

import _ "embed"

// ProvisionAccess creates the enrollment marker and database, schema and membership grants.
//
//go:embed provision-access.sql
var ProvisionAccess string

// InspectAccess returns true only when both Payment logins have exactly the contracted access.
//
//go:embed inspect-access.sql
var InspectAccess string
