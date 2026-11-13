// Package logger retains Document's construction and completion seams over shared logging mechanics.
package logger

import (
	"context"
	"io"
	"log/slog"

	kitlogger "github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
)

func New(output io.Writer, serviceName string, level slog.Level) *slog.Logger {
	return kitlogger.New(output, serviceName, level)
}

func NewIdentity(output io.Writer, identity config.Identity, level slog.Level) *slog.Logger {
	return kitlogger.NewIdentity(output, identity.Identity, level)
}

func LogCompletion(ctx context.Context, log *slog.Logger, level slog.Level, attrs ...any) {
	kitlogger.LogRequired(ctx, log, level, "request completed", attrs...)
}
