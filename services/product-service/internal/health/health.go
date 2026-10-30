// Package health exposes Product's lifecycle-only management probes through servicekit.
package health

import kithealth "github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"

type (
	Check = kithealth.Check
	State = kithealth.State
)

var (
	NewState   = kithealth.NewState
	NewHandler = kithealth.NewHandler
)
