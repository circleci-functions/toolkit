package report

type Level uint32

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
	PanicLevel
	FatalLevel
)

func (level Level) String() string {
	switch level {
	case DebugLevel:
		return "debug"
	case InfoLevel:
		return "info"
	case WarnLevel:
		return "warn"
	case ErrorLevel:
		return "error"
	case PanicLevel:
		return "panic"
	case FatalLevel:
		return "fatal"
	default:
		return "unknown"
	}
}
