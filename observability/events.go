package observability

import (
	"context"
)

// Debug delegates to #Reporter.Debug
func Debug(ctx context.Context, msg string, fields ...Fields) {
	FromContext(ctx).Debug(ctx, msg, fields...)
}

// DebugWithError delegates to #Reporter.DebugWithError
func DebugWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	FromContext(ctx).DebugWithError(ctx, msg, err, fields...)
}

// Info delegates to #Reporter.Info
func Info(ctx context.Context, msg string, fields ...Fields) {
	FromContext(ctx).Info(ctx, msg, fields...)
}

// InfoWithError delegates to #Reporter.InfoWithError
func InfoWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	FromContext(ctx).InfoWithError(ctx, msg, err, fields...)
}

// Warn delegates to #Reporter.Warn
func Warn(ctx context.Context, msg string, fields ...Fields) {
	FromContext(ctx).Warn(ctx, msg, fields...)
}

// WarnWithError delegates to #Reporter.WarnWithError
func WarnWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	FromContext(ctx).WarnWithError(ctx, msg, err, fields...)
}

// Error delegates to #Reporter.Error
func Error(ctx context.Context, msg string, fields ...Fields) {
	FromContext(ctx).Error(ctx, msg, fields...)
}

// ErrorWithError delegates to #Reporter.ErrorWithError
func ErrorWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	FromContext(ctx).ErrorWithError(ctx, msg, err, fields...)
}

// Panic delegates to #Reporter.Panic
func Panic(ctx context.Context, msg string, fields ...Fields) {
	FromContext(ctx).Panic(ctx, msg, fields...)
}

// PanicWithError delegates to #Reporter.PanicWithError
func PanicWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	FromContext(ctx).PanicWithError(ctx, msg, err, fields...)
}

// Fatal delegates to #Reporter.Fatal
func Fatal(ctx context.Context, msg string, fields ...Fields) {
	FromContext(ctx).Fatal(ctx, msg, fields...)
}

// FatalWithError delegates to #Reporter.FatalWithError
func FatalWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	FromContext(ctx).FatalWithError(ctx, msg, err, fields...)
}
