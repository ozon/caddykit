package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"caddykit/internal/logging"
)

func TestRunReconcileLogsFailureAndRecovery(t *testing.T) {
	svc, f := newTestService(t)
	lines := make(chan logEntry, 64)
	svc.Log = func(level logging.Level, format string, args ...any) {
		select {
		case lines <- logEntry{level: level, line: fmt.Sprintf(format, args...)}:
		default:
		}
	}

	f.mu.Lock()
	f.missingServer = true
	f.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.RunReconcile(ctx, 10*time.Millisecond, stop)
	}()
	defer func() {
		close(stop)
		<-done
	}()

	if line := waitForLog(t, lines, 3*time.Second, logging.Warn, "reconcile failed"); line == "" {
		t.Fatal("no reconcile failure was logged at warn")
	}

	f.mu.Lock()
	f.missingServer = false
	f.mu.Unlock()

	if line := waitForLog(t, lines, 3*time.Second, logging.Info, "reconcile recovered"); line == "" {
		t.Fatal("no recovery was logged at info")
	}
}

func waitForLog(t *testing.T, lines <-chan logEntry, timeout time.Duration, level logging.Level, want string) string {
	t.Helper()
	deadline := time.After(timeout)
	var seen []string
	for {
		select {
		case e := <-lines:
			seen = append(seen, fmt.Sprintf("[%s] %s", e.level, e.line))
			if e.level == level && strings.Contains(e.line, want) {
				return e.line
			}
		case <-deadline:
			t.Fatalf("no %s log line containing %q within %s; saw %v", level, want, timeout, seen)
			return ""
		}
	}
}
