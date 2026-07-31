package slog

import (
	"context"
	"encoding"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
	"unicode"
)

const (
	ansiReset              = "\033[0m"
	ansiBold               = "\033[1m"
	ansiResetBold          = "\033[22m"
	ansiFaint              = "\033[2m"
	ansiResetFaint         = "\033[22m"
	ansiBrightRedBold      = "\033[1;91m"
	ansiBrightRedItalic    = "\033[3;91m"
	ansiBrightRedUnderline = "\033[4;91m"
	ansiBrightGreenBold    = "\033[1;92m"
	ansiBrightYellowBold   = "\033[1;93m"
	ansiBrightRedFaint     = "\033[91;2m"
	ansiBrightYellowFaint  = "\033[93;2m"
)

var (
	defaultLevel      = slog.LevelInfo
	defaultTimeFormat = time.StampMilli
)

type options struct {
	// Enable source code location (Default: false)
	AddSource bool

	// Minimum level to log (Default: slog.LevelInfo)
	Level slog.Leveler

	// ReplaceAttr is called to rewrite each non-group attribute before it is logged.
	// See https://pkg.go.dev/log/slog#HandlerOptions for details.
	ReplaceAttr func(groups []string, attr slog.Attr) slog.Attr

	// Time format (Default: time.StampMilli)
	TimeFormat string

	Color bool
}

func newHandler(w io.Writer, opts *options) slog.Handler {
	h := &handler{
		w:          w,
		level:      defaultLevel,
		timeFormat: defaultTimeFormat,
	}
	if opts == nil {
		return h
	}

	h.addSource = opts.AddSource
	if opts.Level != nil {
		h.level = opts.Level
	}
	h.replaceAttr = opts.ReplaceAttr
	if opts.TimeFormat != "" {
		h.timeFormat = opts.TimeFormat
	}
	h.color = opts.Color
	return h
}

type handler struct {
	attrsPrefix string
	groupPrefix string
	groups      []string

	mu sync.Mutex
	w  io.Writer

	addSource   bool
	level       slog.Leveler
	replaceAttr func([]string, slog.Attr) slog.Attr
	timeFormat  string
	color       bool

	traceID string
}

func (h *handler) clone() *handler {
	return &handler{
		attrsPrefix: h.attrsPrefix,
		groupPrefix: h.groupPrefix,
		groups:      h.groups,
		w:           h.w,
		addSource:   h.addSource,
		level:       h.level,
		replaceAttr: h.replaceAttr,
		timeFormat:  h.timeFormat,
		color:       h.color,
	}
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

//nolint:gocognit // This can be in a hot path, probs better to not split it up.
func (h *handler) Handle(_ context.Context, r slog.Record) error {
	// get a buffer from the sync pool
	buf := newBuffer()
	defer buf.Free()

	rep := h.replaceAttr

	// write time
	if !r.Time.IsZero() {
		val := r.Time.Round(0) // strip monotonic to match Attr behavior
		if rep == nil {
			h.appendTime(buf, r.Time)
			_ = buf.WriteByte(' ')
		} else if a := rep(nil /* groups */, slog.Time(slog.TimeKey, val)); a.Key != "" {
			if a.Value.Kind() == slog.KindTime {
				h.appendTime(buf, a.Value.Time())
			} else {
				h.appendValue(buf, a.Value, false)
			}
			_ = buf.WriteByte(' ')
		}
	}

	// write level
	if rep == nil {
		h.appendLevel(buf, r.Level)
		_ = buf.WriteByte(' ')
	} else if a := rep(nil /* groups */, slog.Any(slog.LevelKey, r.Level)); a.Key != "" {
		h.appendValue(buf, a.Value, false)
		_ = buf.WriteByte(' ')
	}

	traceID := h.traceID
	if traceID == "" {
		r.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "trace_id" {
				traceID = attr.Value.String()
				return false
			}
			return true
		})
	}
	if traceID == "" {
		h.appendColorString(buf, "unkwn")
	} else {
		h.appendColorString(buf, traceID[len(traceID)-5:])
	}
	_ = buf.WriteByte(' ')

	// write source
	if h.addSource {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := fs.Next()
		if f.File != "" {
			src := &slog.Source{
				Function: f.Function,
				File:     f.File,
				Line:     f.Line,
			}

			if rep == nil {
				h.appendSource(buf, src)
				_ = buf.WriteByte(' ')
			} else if a := rep(nil /* groups */, slog.Any(slog.SourceKey, src)); a.Key != "" {
				h.appendValue(buf, a.Value, false)
				_ = buf.WriteByte(' ')
			}
		}
	}

	// write message
	if rep == nil {
		h.appendBoldString(buf, r.Message)
		_ = buf.WriteByte(' ')
	} else if a := rep(nil /* groups */, slog.String(slog.MessageKey, r.Message)); a.Key != "" {
		h.appendValue(buf, a.Value, false)
		_ = buf.WriteByte(' ')
	}

	// write handler attributes
	if len(h.attrsPrefix) > 0 {
		_, _ = buf.WriteString(h.attrsPrefix)
	}

	// write attributes
	r.Attrs(func(attr slog.Attr) bool {
		h.appendAttr(buf, attr, h.groupPrefix, h.groups)
		return true
	})

	if len(*buf) == 0 {
		return nil
	}
	(*buf)[len(*buf)-1] = '\n' // replace last space with newline

	h.mu.Lock()
	defer h.mu.Unlock()

	_, err := h.w.Write(*buf)
	return err
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	h2 := h.clone()

	buf := newBuffer()
	defer buf.Free()

	// write attributes to buffer
	for _, attr := range attrs {
		if attr.Key == "trace_id" {
			h2.traceID = attr.Value.String()
			continue
		}
		h.appendAttr(buf, attr, h.groupPrefix, h.groups)
	}
	h2.attrsPrefix = h.attrsPrefix + string(*buf)
	return h2
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h2 := h.clone()
	h2.groupPrefix += name + "."
	h2.groups = append(h2.groups, name)
	return h2
}

func (h *handler) appendTime(buf *buffer, t time.Time) {
	_, _ = buf.WriteStringIf(h.color, ansiFaint)
	*buf = t.AppendFormat(*buf, h.timeFormat)
	_, _ = buf.WriteStringIf(h.color, ansiReset)
}

func (h *handler) appendLevel(buf *buffer, level slog.Level) {
	switch {
	case level < slog.LevelInfo:
		_, _ = buf.WriteString("DBG")
		appendLevelDelta(buf, level-slog.LevelDebug)
	case level < slog.LevelWarn:
		_, _ = buf.WriteStringIf(h.color, ansiBrightGreenBold)
		_, _ = buf.WriteString("INF")
		appendLevelDelta(buf, level-slog.LevelInfo)
		_, _ = buf.WriteStringIf(h.color, ansiReset)
	case level < slog.LevelError:
		_, _ = buf.WriteStringIf(h.color, ansiBrightYellowBold)
		_, _ = buf.WriteString("WRN")
		appendLevelDelta(buf, level-slog.LevelWarn)
		_, _ = buf.WriteStringIf(h.color, ansiReset)
	case level < levelFatal:
		_, _ = buf.WriteStringIf(h.color, ansiBrightRedBold)
		_, _ = buf.WriteString("ERR")
		appendLevelDelta(buf, level-slog.LevelError)
		_, _ = buf.WriteStringIf(h.color, ansiReset)
	case level < levelPanic:
		_, _ = buf.WriteStringIf(h.color, ansiBrightRedUnderline)
		_, _ = buf.WriteString("FAT")
		appendLevelDelta(buf, level-levelFatal)
		_, _ = buf.WriteStringIf(h.color, ansiReset)
	default:
		_, _ = buf.WriteStringIf(h.color, ansiBrightRedItalic)
		_, _ = buf.WriteString("PAN")
		appendLevelDelta(buf, level-levelPanic)
		_, _ = buf.WriteStringIf(h.color, ansiReset)
	}
}

func appendLevelDelta(buf *buffer, delta slog.Level) {
	if delta == 0 {
		return
	} else if delta > 0 {
		_ = buf.WriteByte('+')
	}
	*buf = strconv.AppendInt(*buf, int64(delta), 10)
}

func (h *handler) appendSource(buf *buffer, src *slog.Source) {
	dir, file := filepath.Split(src.File)

	_, _ = buf.WriteStringIf(h.color, ansiFaint)
	_, _ = buf.WriteString(filepath.Join(filepath.Base(dir), file))
	_ = buf.WriteByte(':')
	_, _ = buf.WriteString(strconv.Itoa(src.Line))
	_, _ = buf.WriteStringIf(h.color, ansiReset)
}

func (h *handler) appendAttr(buf *buffer, attr slog.Attr, groupsPrefix string, groups []string) {
	attr.Value = attr.Value.Resolve()
	if rep := h.replaceAttr; rep != nil && attr.Value.Kind() != slog.KindGroup {
		attr = rep(groups, attr)
		attr.Value = attr.Value.Resolve()
	}

	if attr.Equal(slog.Attr{}) {
		return
	}

	switch attr.Key {
	case "canary", "deploy_environment", "level_sys", "request_id", "span_id", "parent_span_id", "result", "revision", "trace_id",
		// These are replaced with prefixed attributes automatically.
		"user_id", "project_id", "project_name", "org_id", "org_name", "pipeline_id", "workflow_id", "job_id",
		// HTTP server attributes.
		"client.address", "http.remote_ip", "user_agent.original", "http.path", "http.host", "http.url", "url.scheme",
		"http.status_code", "http.method",
		// HTTP client attributes.
		"backplane.client.name", "http.base_url", "http.request.resend_count", "http.request.body.size",
		"http.response.header.content-type", "req.con_new", "req.con_wait", "req.first_byte", "req.wrote_request",
		"http.response_content_type", "http.retry", "http.request_content_length", "http.response_content_length":
		return
	case "warning":
		h.appendWarning(buf, attr.Value.String(), groupsPrefix)
		_ = buf.WriteByte(' ')
	case "error":
		h.appendError(buf, attr.Value.String(), groupsPrefix)
		_ = buf.WriteByte(' ')
	default:
		if attr.Value.Kind() == slog.KindGroup {
			if attr.Key != "" {
				groupsPrefix += attr.Key + "."
				groups = append(groups, attr.Key)
			}
			for _, groupAttr := range attr.Value.Group() {
				h.appendAttr(buf, groupAttr, groupsPrefix, groups)
			}
		} else {
			h.appendKey(buf, attr.Key, groupsPrefix)
			h.appendValue(buf, attr.Value, true)
			_ = buf.WriteByte(' ')
		}
	}

}

func (h *handler) appendKey(buf *buffer, key, groups string) {
	_, _ = buf.WriteStringIf(h.color, ansiFaint)
	appendString(buf, groups+key, true)
	_ = buf.WriteByte('=')
	_, _ = buf.WriteStringIf(h.color, ansiReset)
}

func (h *handler) appendValue(buf *buffer, v slog.Value, quote bool) {
	switch v.Kind() {
	case slog.KindString:
		appendString(buf, v.String(), quote)
	case slog.KindInt64:
		*buf = strconv.AppendInt(*buf, v.Int64(), 10)
	case slog.KindUint64:
		*buf = strconv.AppendUint(*buf, v.Uint64(), 10)
	case slog.KindFloat64:
		*buf = strconv.AppendFloat(*buf, v.Float64(), 'g', -1, 64)
	case slog.KindBool:
		*buf = strconv.AppendBool(*buf, v.Bool())
	case slog.KindDuration:
		appendString(buf, v.Duration().String(), quote)
	case slog.KindTime:
		appendString(buf, v.Time().String(), quote)
	case slog.KindAny:
		switch cv := v.Any().(type) {
		case slog.Level:
			h.appendLevel(buf, cv)
		case encoding.TextMarshaler:
			data, err := cv.MarshalText()
			if err != nil {
				break
			}
			appendString(buf, string(data), quote)
		case *slog.Source:
			h.appendSource(buf, cv)
		default:
			appendString(buf, fmt.Sprintf("%+v", v.Any()), quote)
		}
	case slog.KindGroup, slog.KindLogValuer:
		// no-op
	}
}

func (h *handler) appendError(buf *buffer, err string, groupsPrefix string) {
	_, _ = buf.WriteStringIf(h.color, ansiBrightRedFaint)
	appendString(buf, groupsPrefix+"error", true)
	_ = buf.WriteByte('=')
	_, _ = buf.WriteStringIf(h.color, ansiResetFaint)
	appendString(buf, err, true)
	_, _ = buf.WriteStringIf(h.color, ansiReset)
}

func (h *handler) appendWarning(buf *buffer, err string, groupsPrefix string) {
	_, _ = buf.WriteStringIf(h.color, ansiBrightYellowFaint)
	appendString(buf, groupsPrefix+"warning", true)
	_ = buf.WriteByte('=')
	_, _ = buf.WriteStringIf(h.color, ansiResetFaint)
	appendString(buf, err, true)
	_, _ = buf.WriteStringIf(h.color, ansiReset)
}

func (h *handler) appendColorString(buf *buffer, value string) {
	//#nosec:G115 // fixed size slice
	i := crc32.Checksum([]byte(value), crc32.IEEETable) % uint32(len(colors))
	_, _ = buf.WriteStringIf(h.color, fmt.Sprintf("\033[1;38;5;%dm", colors[i]))
	appendString(buf, value, false)
	_, _ = buf.WriteStringIf(h.color, ansiReset)
}

func (h *handler) appendBoldString(buf *buffer, value string) {
	_, _ = buf.WriteStringIf(h.color, ansiBold)
	appendString(buf, value, false)
	_, _ = buf.WriteStringIf(h.color, ansiResetBold)
}

// colors is all ansi colour codes that look ok against black
var colors = []uint8{
	1, 2, 3, 4, 5, 6, 9, 10, 11, 12, 13, 14, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39,
	40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79,
	80, 81, 82, 83, 84, 85, 86, 87, 92, 93, 94, 95, 96, 97, 98, 99, 100, 101, 102, 103, 104, 105, 106, 107, 108, 109,
	110, 111, 112, 113, 114, 115, 116, 117, 118, 119, 120, 121, 122, 123, 124, 125, 126, 127, 128, 129, 130, 131, 132,
	133, 134, 135, 136, 137, 138, 139, 140, 141, 142, 143, 144, 146, 147, 148, 149, 150, 151, 152, 153, 154, 155, 156,
	157, 158, 159, 160, 161, 162, 163, 164, 165, 166, 167, 168, 169, 170, 171, 172, 173, 174, 175, 176, 177, 178, 179,
	180, 181, 182, 183, 184, 185, 186, 187, 188, 189, 190, 191, 192, 193, 194, 195, 196, 197, 198, 199, 200, 201, 202,
	203, 204, 205, 206, 207, 208, 209, 210, 211, 212, 213, 214, 215, 216, 217, 218, 219, 220, 221, 222, 223, 224, 225,
	226, 227, 228, 229, 230,
}

func appendString(buf *buffer, s string, quote bool) {
	if quote && needsQuoting(s) {
		*buf = strconv.AppendQuote(*buf, s)
	} else {
		_, _ = buf.WriteString(s)
	}
}

func needsQuoting(s string) bool {
	if len(s) == 0 {
		return true
	}
	for _, r := range s {
		if unicode.IsSpace(r) || r == '"' || r == '=' || !unicode.IsPrint(r) {
			return true
		}
	}
	return false
}
