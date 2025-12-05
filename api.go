package net

import "github.com/ndsky1003/net/logger"

func SetGlobalLogger(l logger.Logger) {
	logger.SetGlobalLogger(l)
}
