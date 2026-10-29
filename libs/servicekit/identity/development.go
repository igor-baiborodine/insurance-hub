package identity

import "strconv"

// DevelopmentIdentity returns the shared development defaults for a service.
// The caller supplies the process ID at its chosen capture point and retains
// responsibility for environment overrides and service-specific validation.
func DevelopmentIdentity(serviceName string, processID int) Identity {
	return Identity{
		ServiceNamespace: "insurance-hub",
		ServiceName:      serviceName,
		ServiceVersion:   "development",
		ServiceInstance:  "local-" + strconv.Itoa(processID),
		Environment:      "development",
		Implementation:   "go",
	}
}
