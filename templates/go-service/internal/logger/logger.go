// Package logger retains the scaffold's completion-log contract over shared logging mechanics.
package logger

import (
	"context"
	"log/slog"

	kitlogger "github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"
)

func LogCompletion(ctx context.Context, log *slog.Logger, level slog.Level, attrs ...any) {
	kitlogger.LogRequired(ctx, log, level, "request completed", attrs...)
}
