package identity

// Identity contains application-owned resource and log fields. Collectors can add
// infrastructure fields. This value neither loads settings nor validates policy:
// the service owns namespace, implementation, environment, version and instance rules.
type Identity struct {
	ServiceNamespace string
	ServiceName      string
	ServiceVersion   string
	ServiceInstance  string
	Environment      string
	Implementation   string
}
