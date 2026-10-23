// Package logging is the leveled logger shared by the CLI and the web process.
package logging

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	Off Level = iota
	Error
	Warn
	Info
	Debug
)

func (l Level) String() string {
	switch l {
	case Off:
		return "off"
	case Error:
		return "error"
	case Warn:
		return "warn"
	case Info:
		return "info"
	case Debug:
		return "debug"
	}
	return "unknown"
}

// ParseLevel maps a --log-level value to a Level.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "none":
		return Off, nil
	case "error":
		return Error, nil
	case "warn", "warning":
		return Warn, nil
	case "info":
		return Info, nil
	case "debug":
		return Debug, nil
	}
	return 0, fmt.Errorf("unknown log level %q (use off, error, warn, info or debug)", s)
}

// Logger writes timestamped lines at or below its level.
type Logger struct {
	mu  sync.Mutex
	out io.Writer
	lvl Level
}

func New(out io.Writer, lvl Level) *Logger {
	if out == nil {
		out = os.Stderr
	}
	return &Logger{out: out, lvl: lvl}
}

func (l *Logger) Level() Level {
	if l == nil {
		return Off
	}
	return l.lvl
}

func (l *Logger) Enabled(lvl Level) bool {
	return l != nil && lvl != Off && l.lvl >= lvl
}

func (l *Logger) Log(lvl Level, format string, args ...any) {
	if !l.Enabled(lvl) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.out, "caddykit: %s %s\n", time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf(format, args...))
}

func (l *Logger) Errorf(format string, args ...any) { l.Log(Error, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.Log(Warn, format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.Log(Info, format, args...) }
func (l *Logger) Debugf(format string, args ...any) { l.Log(Debug, format, args...) }

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// FromFlags resolves a logger from --log-level and --log-file values. An empty
// level keeps def; an empty file keeps stderr. A disabled logger never creates
// the log file.
func FromFlags(level, file string, def Level) (*Logger, io.Closer, error) {
	lvl := def
	if strings.TrimSpace(level) != "" {
		parsed, err := ParseLevel(level)
		if err != nil {
			return nil, nil, err
		}
		lvl = parsed
	}
	if lvl == Off {
		return New(io.Discard, Off), nopCloser{}, nil
	}
	var out io.Writer = os.Stderr
	var closer io.Closer = nopCloser{}
	if strings.TrimSpace(file) != "" {
		f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file: %w", err)
		}
		out = f
		closer = f
	}
	return New(out, lvl), closer, nil
}
