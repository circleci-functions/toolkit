package report

// LogEmitter emits logs
type LogEmitter interface {
	Log(level Level, msg string, fields map[string]any)
}

func SysLevel(level Level) int {
	switch level {
	case DebugLevel:
		return 7
	case InfoLevel:
		return 6
	case WarnLevel:
		return 4
	case ErrorLevel:
		return 3
	case PanicLevel:
		return 1
	case FatalLevel:
		return 0
	default:
		return -1
	}
}

type LogFormat string

const (
	JSONLogFormat  LogFormat = "json"
	ColorLogFormat LogFormat = "color"
	TextLogFormat  LogFormat = "text"
)
