package net

import (
	"log/slog"

	"github.com/ndsky1003/net/log"
)

func SetLogger(l *slog.Logger) {
	log.SetLogger(l)
}
