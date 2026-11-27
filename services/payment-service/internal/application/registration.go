package application

import "time"

// PolicyRegistration contains the validated facts needed to open one Payment account.
type PolicyRegistration struct {
	PolicyNumber  string
	Premium       string
	CoverageStart time.Time
}
