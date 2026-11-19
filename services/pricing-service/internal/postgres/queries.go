package postgres

import _ "embed"

//go:embed queries/assume-owner.sql
var assumeOwnerQuery string
