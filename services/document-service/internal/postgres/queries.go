package postgres

import _ "embed"

//go:embed queries/assume-owner.sql
var assumeOwnerQuery string

//go:embed queries/administration-settings.sql
var administrationSettingsQuery string
