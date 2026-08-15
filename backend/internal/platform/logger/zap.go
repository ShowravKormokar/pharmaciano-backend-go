package logger

import (
	"backend/internal/platform/config"
	"context"
	"os"
	"strings"
	"time"

	"github.com/natefinch/lumberjack"

	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// contextKey is unexported to avoid collisions
type contextKey int

const ctxKeyLogger contextKey = iota + 1

// New builds a Zap logger from config. Production uses JSON to stdout; dev uses colourised console output
func New(cfg config.LoggingConfig, appName, appVersion string) (*zap.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "ts"
	encCfg.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	encCfg.EncodeDuration = zapcore.StringDurationEncoder
	encCfg.EncodeCaller = zapcore.ShortCallerEncoder
	encCfg.EncodeLevel = zapcore.LowercaseLevelEncoder
	encCfg.MessageKey = "msg"
	encCfg.LevelKey = "level"

	var encoder zapcore.Encoder
	switch strings.ToLower(cfg.Format) {
	case "console":
		encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encCfg)
	default:
		encoder = zapcore.NewJSONEncoder(encCfg)
	}

	sink := zapcore.AddSync(os.Stdout)
	if strings.ToLower(cfg.Output) == "file" && cfg.FilePath != "" {
		sink = zapcore.AddSync(&lumberjack.Logger{
			Filename:   cfg.FilePath,
			MaxSize:    100, // MB
			MaxBackups: 10,
			MaxAge:     30, // days
			Compress:   true,
		})
	}

	core := zapcore.NewCore(encoder, sink, level)

	if cfg.Sampling.Enabled && cfg.Sampling.Initial > 0 {
		core = zapcore.NewSamplerWithOptions(core, time.Second, cfg.Sampling.Initial, cfg.Sampling.Thereafter)
	}

	logger := zap.New(core,
		zap.AddCaller(),
		zap.AddStacktrace(zap.ErrorLevel),
		zap.Fields(
			zap.String("app", appName),
			zap.String("version", appVersion),
			zap.String("env", strings.ToLower(os.Getenv("APP_ENV"))),
		),
	)

	if len(cfg.RedactFields) > 0 {
		logger = logger.With(zap.Strings("_redact", cfg.RedactFields))
	}
	return logger, nil
}

func WithContext(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, ctxKeyLogger, l)
}

// FromContext returns the request-scoped logger, or the global one if absent.
func FromContext(ctx context.Context) *zap.Logger {
	if l, ok := ctx.Value(ctxKeyLogger).(*zap.Logger); ok && l != nil {
		return l
	}
	return zap.L()
}

func With(ctx context.Context, fields ...zap.Field) *zap.Logger {
	return FromContext(ctx).With(fields...)
}

func parseLevel(s string) (zapcore.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return zap.DebugLevel, nil
	case "", "info":
		return zap.InfoLevel, nil
	case "warn", "warning":
		return zap.WarnLevel, nil
	case "error":
		return zap.ErrorLevel, nil
	case "fatal":
		return zap.FatalLevel, nil
	default:
		return 0, fmt.Errorf("logger: invalid level %q", s)
	}
}
