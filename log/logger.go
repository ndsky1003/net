package log

import (
	"log/slog"
	"sync/atomic"
)

var defaultLogger atomic.Pointer[slog.Logger]

func init() {
	defaultLogger.Store(slog.Default())
}

func Logger() *slog.Logger {
	return defaultLogger.Load()
}

func SetLogger(l *slog.Logger) {
	defaultLogger.Store(l)
}
