// Package applog builds the application logger with a runtime-adjustable level
// and an in-memory ring buffer of recent records for display in the app's
// diagnostics view.
package applog

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Entry is a captured log record shown in the diagnostics view.
type Entry struct {
	Time    time.Time
	Level   string
	Message string
	Attrs   string
}

const bufferSize = 300

// Controller adjusts the active log level at runtime and exposes recent records.
type Controller struct {
	level *slog.LevelVar

	mu    sync.Mutex
	buf   []Entry
	next  int
	count int
}

// New builds a logger whose level can change at runtime and whose records are
// captured into a bounded ring buffer. It logs JSON in production, text otherwise.
func New(env string) (*slog.Logger, *Controller) {
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelInfo)
	c := &Controller{level: lv, buf: make([]Entry, bufferSize)}

	opts := &slog.HandlerOptions{Level: lv}
	var inner slog.Handler
	if strings.EqualFold(env, "production") || strings.EqualFold(env, "prod") {
		inner = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		inner = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(&tapHandler{inner: inner, ctrl: c}), c
}

// SetLevel updates the active log level for both stdout and the buffer.
func (c *Controller) SetLevel(l slog.Level) { c.level.Set(l) }

// LevelName returns the active level as a lower-case name (debug/info/warn/error).
func (c *Controller) LevelName() string {
	return strings.ToLower(c.level.Level().String())
}

// Levels lists the selectable level names in increasing severity.
func Levels() []string { return []string{"debug", "info", "warn", "error"} }

// ParseLevel maps a name to a slog.Level and reports whether it was valid.
func ParseLevel(name string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}

// Recent returns up to n most-recent entries, newest first.
func (c *Controller) Recent(n int) []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n <= 0 || n > c.count {
		n = c.count
	}
	out := make([]Entry, 0, n)
	idx := c.next - 1
	for i := 0; i < n; i++ {
		if idx < 0 {
			idx += len(c.buf)
		}
		out = append(out, c.buf[idx])
		idx--
	}
	return out
}

func (c *Controller) add(e Entry) {
	c.mu.Lock()
	c.buf[c.next] = e
	c.next = (c.next + 1) % len(c.buf)
	if c.count < len(c.buf) {
		c.count++
	}
	c.mu.Unlock()
}

var lineSanitizer = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")

// clean strips control characters (log-injection defense) and caps the length.
func clean(s string, maxRunes int) string {
	s = lineSanitizer.Replace(s)
	if r := []rune(s); len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return s
}

// tapHandler forwards records to an inner handler and captures them for the UI.
type tapHandler struct {
	inner slog.Handler
	ctrl  *Controller
	attrs string
}

func (h *tapHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *tapHandler) Handle(ctx context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.WriteString(h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(a.Key)
		sb.WriteByte('=')
		sb.WriteString(a.Value.String())
		return true
	})
	h.ctrl.add(Entry{
		Time:    r.Time,
		Level:   strings.ToLower(r.Level.String()),
		Message: clean(r.Message, 300),
		Attrs:   clean(sb.String(), 1000),
	})
	return h.inner.Handle(ctx, r)
}

func (h *tapHandler) WithAttrs(as []slog.Attr) slog.Handler {
	var sb strings.Builder
	sb.WriteString(h.attrs)
	for _, a := range as {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(a.Key)
		sb.WriteByte('=')
		sb.WriteString(a.Value.String())
	}
	return &tapHandler{inner: h.inner.WithAttrs(as), ctrl: h.ctrl, attrs: sb.String()}
}

func (h *tapHandler) WithGroup(name string) slog.Handler {
	return &tapHandler{inner: h.inner.WithGroup(name), ctrl: h.ctrl, attrs: h.attrs}
}
