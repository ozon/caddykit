package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRunReconcileLogsFailureAndRecovery(t *testing.T) {
	svc, f := newTestService(t)
	lines := make(chan string, 64)
	svc.Logf = func(format string, args ...any) {
		select {
		case lines <- fmt.Sprintf(format, args...):
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

	if line := waitForLog(t, lines, 3*time.Second, "reconcile failed"); line == "" {
		t.Fatal("no reconcile failure was logged")
	}

	f.mu.Lock()
	f.missingServer = false
	f.mu.Unlock()

	if line := waitForLog(t, lines, 3*time.Second, "reconcile recovered"); line == "" {
		t.Fatal("no recovery was logged")
	}
}

func waitForLog(t *testing.T, lines <-chan string, timeout time.Duration, want string) string {
	t.Helper()
	deadline := time.After(timeout)
	var seen []string
	for {
		select {
		case line := <-lines:
			seen = append(seen, line)
			if strings.Contains(line, want) {
				return line
			}
		case <-deadline:
			t.Fatalf("no log line containing %q within %s; saw %v", want, timeout, seen)
			return ""
		}
	}
}
