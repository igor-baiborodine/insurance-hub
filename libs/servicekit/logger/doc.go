// Package logger owns structured JSON logging, sensitive attribute redaction and trace
// correlation. It sanitizes attributes, including nested structured values, but does not inspect
// or rewrite message strings.
package logger
