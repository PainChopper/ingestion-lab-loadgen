package main

import (
	"io"
	"os"

	zaplogfmt "github.com/jsternberg/zap-logfmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func newApplicationLogger(level string, output io.Writer) (*zap.Logger, error) {
	var atomicLevel zapcore.Level
	if err := atomicLevel.UnmarshalText([]byte(level)); err != nil {
		return nil, err
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "ts"
	encoderConfig.LevelKey = "level"
	encoderConfig.MessageKey = "msg"
	encoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder
	encoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder

	core := zapcore.NewCore(
		zaplogfmt.NewEncoder(encoderConfig),
		zapcore.AddSync(output),
		atomicLevel,
	)
	return zap.New(core), nil
}

func newStdoutApplicationLogger(level string) (*zap.Logger, error) {
	return newApplicationLogger(level, os.Stdout)
}

func loggerOrNop(loggers []*zap.Logger) *zap.Logger {
	if len(loggers) == 1 && loggers[0] != nil {
		return loggers[0]
	}
	return zap.NewNop()
}
