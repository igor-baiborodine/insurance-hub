// Package provisioning holds the platform access SQL that the disposable suites run against
// PostgreSQL containers, matching the access an enrolled Payment CNPG target must have.
package provisioning

import _ "embed"

// ProvisionAccess creates the enrollment marker and database, schema and membership grants.
//
//go:embed provision-access.sql
var ProvisionAccess string
