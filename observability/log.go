package observability

import (
	"context"
	"strings"
)

type loggerNameKey struct{}

func getLoggerName(ctx context.Context) (string, bool) {
	val := ctx.Value(loggerNameKey{})
	if val != nil {
		return val.(string), true
	}
	return "", false
}

// AppendLoggerName appends a logger name to the current context.  Multiple
// calls to AppendLoggerName builds a hierarchical logger name, with `.` as the
// separator.
func AppendLoggerName(ctx context.Context, loggerName string) context.Context {
	loggerNames := []string{}
	if name, ok := getLoggerName(ctx); ok {
		loggerNames = append(loggerNames, name)
	}
	loggerNames = append(loggerNames, loggerName)

	newLoggerName := strings.Join(loggerNames, ".")
	return context.WithValue(ctx, loggerNameKey{}, newLoggerName)
}
