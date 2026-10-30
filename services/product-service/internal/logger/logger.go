// Package logger retains Product's construction seam over shared logging mechanics.
package logger

import (
	"io"
	"log/slog"

	kitlogger "github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
)

func New(output io.Writer, serviceName string, level slog.Level) *slog.Logger {
	return kitlogger.New(output, serviceName, level)
}

func NewIdentity(output io.Writer, identity config.Identity, level slog.Level) *slog.Logger {
	return kitlogger.NewIdentity(output, identity, level)
}
