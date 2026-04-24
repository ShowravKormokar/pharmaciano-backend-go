package logger

import (
	"log"

	"backend/internal/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.Logger

func Init() {
	var cfg zap.Config
	if config.Cfg.AppEnv == "production" {
		cfg = zap.NewProductionConfig()
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	} else {
		cfg = zap.NewDevelopmentConfig()
	}
	logger, err := cfg.Build()
	if err != nil {
		log.Fatalf("❌ logger init: %v", err)
	}
	Log = logger
	Log.Info("✅ Logger initialized")
}
