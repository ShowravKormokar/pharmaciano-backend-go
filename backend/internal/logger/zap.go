package logger

import (
	"log"

	"backend/internal/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.Logger

func InitLogger() {
	cfg := config.Cfg

	var zapCfg zap.Config

	// Environment based config
	if cfg.AppPort == "8080" {
		zapCfg = zap.NewDevelopmentConfig()
	} else {
		zapCfg = zap.NewProductionConfig()
	}

	// Time format
	zapCfg.EncoderConfig.TimeKey = "time"
	zapCfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger, err := zapCfg.Build()
	if err != nil {
		log.Fatal("❌ Failed to initialize logger:", err)
	}

	Log = logger
	Log.Info("✅ Logger initialized")
}
