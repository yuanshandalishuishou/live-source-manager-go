package web

import "live-source-manager-go/internal/logger"

func loggerInfo(format string, args ...any) { logger.L().Info(format, args...) }
func loggerWarn(format string, args ...any) { logger.L().Warning(format, args...) }
func loggerErr(format string, args ...any)  { logger.L().Error(format, args...) }
