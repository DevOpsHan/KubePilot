// Package logging 提供统一的日志器构造。
package logging

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New 创建日志器。本地开发使用易读的彩色控制台输出。
func New() (*zap.Logger, error) {
	cfg := zap.NewDevelopmentConfig()
	cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	return cfg.Build()
}
