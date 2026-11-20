package postgres

import _ "embed"

//go:embed queries/assume-owner.sql
var assumeOwnerQuery string

//go:embed queries/inspect-reader-access.sql
var inspectReaderAccessQuery string

//go:embed queries/read-tariff.sql
var readTariffQuery string
