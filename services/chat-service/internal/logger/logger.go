// Package logger retains Chat's safe lifecycle-log contract over shared logging mechanics.
package logger

import (
	"context"
	"log/slog"

	kitlogger "github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"
)

func LogHandshake(ctx context.Context, log *slog.Logger, level slog.Level, attrs ...any) {
	logRequired(ctx, log, level, "chat WebSocket handshake completed", attrs...)
}

func LogConnectionOpened(log *slog.Logger) {
	logRequired(context.Background(), log, slog.LevelInfo, "chat WebSocket connection opened")
}

func LogConnectionClosed(log *slog.Logger, attrs ...any) {
	logRequired(
		context.Background(),
		log,
		slog.LevelInfo,
		"chat WebSocket connection closed",
		attrs...,
	)
}

func LogBroadcastFailure(log *slog.Logger, attrs ...any) {
	logRequired(
		context.Background(),
		log,
		slog.LevelWarn,
		"chat broadcast delivery failed",
		attrs...)
}

func LogShutdownFailure(ctx context.Context, log *slog.Logger) {
	logRequired(ctx, log, slog.LevelError, "chat service shutdown failed")
}

func logRequired(
	ctx context.Context,
	log *slog.Logger,
	level slog.Level,
	message string,
	attrs ...any,
) {
	kitlogger.LogRequired(ctx, log, level, message, attrs...)
}
