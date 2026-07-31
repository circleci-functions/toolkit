package report

import "fmt"

type ErrorEmitter interface {
	SendDeployment()
	ReportError(level Level, err error, extra map[string]any)
	Close() error
}

// LogToError lets you use a LogEmitter as an ErrorEmitter
type LogToError struct {
	LogEmitter LogEmitter
}

var _ ErrorEmitter = (*LogToError)(nil)

func (l *LogToError) SendDeployment() {}

func (l *LogToError) ReportError(level Level, err error, extra map[string]any) {
	msg := fmt.Sprintf("error report: %s", err.Error())
	l.LogEmitter.Log(level, msg, extra)
}

func (l *LogToError) Close() error {
	return nil
}
