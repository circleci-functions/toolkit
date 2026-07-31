// Package slog is a low-level wrapper for slog
//
//nolint:revive // dont use this
package slog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/circleci-functions/toolkit/internal/report"
)

// stdExitFn exists for testing purposes only
var stdExitFn = os.Exit

func init() { //nolint:gochecknoinits // This is the way to alter std log output from 3rd party code.
	l := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		ReplaceAttr: customFieldMap,
	}))
	slog.SetDefault(l)
}

type Logger struct {
	cfg    LoggerConfig
	logger *slog.Logger
}

// confirm we satisfy report.LogEmitter
var _ report.LogEmitter = &Logger{}

type LoggerConfig struct {
	Level  report.Level
	Format report.LogFormat
	Output io.Writer
}

func NewLogger(cfg LoggerConfig) (*Logger, error) {
	var level slog.Level
	err := level.UnmarshalText([]byte(cfg.Level.String()))
	if err != nil {
		return nil, err
	}

	if cfg.Output == nil {
		cfg.Output = os.Stdout
	}

	var h slog.Handler
	switch cfg.Format {
	case report.JSONLogFormat:
		h = slog.NewJSONHandler(cfg.Output, &slog.HandlerOptions{
			Level:       level,
			ReplaceAttr: customFieldMap,
		})
	case report.ColorLogFormat:
		h = newHandler(cfg.Output, &options{
			Level:      level,
			TimeFormat: time.TimeOnly,
			Color:      true,
		})
	case report.TextLogFormat:
		h = newHandler(cfg.Output, &options{
			Level:      level,
			TimeFormat: time.TimeOnly,
			Color:      false,
		})
	default:
		return nil, fmt.Errorf("unsupported log format %s", cfg.Format)
	}

	return &Logger{
		cfg:    cfg,
		logger: slog.New(h),
	}, nil
}

func (l *Logger) Log(level report.Level, msg string, fields map[string]any) {
	var lvl slog.Level
	switch level {
	case report.DebugLevel:
		lvl = slog.LevelDebug
	case report.InfoLevel:
		lvl = slog.LevelInfo
	case report.WarnLevel:
		lvl = slog.LevelWarn
	case report.ErrorLevel:
		lvl = slog.LevelError
	case report.FatalLevel:
		lvl = levelFatal
	case report.PanicLevel:
		lvl = levelPanic
	}

	ctx := context.Background()
	if l.logger.Enabled(ctx, lvl) {
		l.logger.LogAttrs(ctx, lvl, msg, l.toAttrs(level, fields)...)
	}

	//nolint:exhaustive // only want to check these cases
	switch level {
	case report.PanicLevel:
		panic(msg)
	case report.FatalLevel:
		stdExitFn(1)
	default:
	}
}

func (l *Logger) toAttrs(level report.Level, fields map[string]any) []slog.Attr {
	fields[levelSysName] = report.SysLevel(level)

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	attrs := make([]slog.Attr, 0, len(fields))
	for _, k := range keys {
		attrs = append(attrs, slog.Any(k, fields[k]))
	}
	return attrs
}

const (
	levelFatal slog.Level = 12
	levelPanic slog.Level = 16
)

const (
	levelSysName = "level_sys"
	levelName    = "level_name"
)

var (
	valueDebug   = slog.StringValue("debug")
	valueInfo    = slog.StringValue("info")
	valueWarning = slog.StringValue("warning")
	valueError   = slog.StringValue("error")
	valueFatal   = slog.StringValue("fatal")
	valuePanic   = slog.StringValue("panic")
)

func customFieldMap(groups []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.LevelKey:
		a.Key = levelName
		switch a.Value.String() {
		case "DEBUG":
			a.Value = valueDebug
		case "INFO":
			a.Value = valueInfo
		case "WARN":
			a.Value = valueWarning
		case "ERROR":
			a.Value = valueError
		case "ERROR+4":
			a.Value = valueFatal
		case "ERROR+8":
			a.Value = valuePanic
		}
	case levelSysName:
		a.Key = slog.LevelKey
	default:
		if a.Value.Kind() == slog.KindAny {
			v := a.Value.Any()
			a.Value = slog.StringValue(fmt.Sprintf("%+v", v))
		}
	}

	return a
}
