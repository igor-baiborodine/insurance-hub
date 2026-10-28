// Package telemetry owns explicit OpenTelemetry provider lifecycle, propagation configuration,
// safe export diagnostics and Go runtime instruments. Callers retain ownership of business
// instruments, transport instrumentation and process-global diagnostics installation.
package telemetry
