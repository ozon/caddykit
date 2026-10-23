package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"caddykit/internal/logging"
	"caddykit/internal/model"
)

type logEntry struct {
	level logging.Level
	line  string
}

func TestOperationLogApplyUndoAndFailure(t *testing.T) {
	svc, _ := newTestService(t)
	var lines []logEntry
	svc.Log = func(level logging.Level, format string, args ...any) {
		lines = append(lines, logEntry{level: level, line: fmt.Sprintf(format, args...)})
	}
	ctx := context.Background()

	p := mustPreview(svc.PreviewCreate(ctx, model.Spec{Host: "log.example.com", Upstream: "127.0.0.1:3000"}, "create log route", nil))
	res, err := svc.Apply(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLog(lines, logging.Info, "apply ok: kind=create route="+res.RouteID) {
		t.Fatalf("apply success was not logged at info; entries: %v", lines)
	}

	lines = nil
	if _, err := svc.Apply(ctx, p); err == nil {
		t.Fatal("second apply of the same preview should fail")
	}
	if !hasLog(lines, logging.Error, "apply failed:") || !hasLog(lines, logging.Error, "preview is stale") {
		t.Fatalf("apply failure was not logged at error with its cause; entries: %v", lines)
	}

	lines = nil
	undoRes, err := svc.Undo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLog(lines, logging.Info, "undo ok: kind=undo route="+undoRes.RouteID) {
		t.Fatalf("undo was not logged at info; entries: %v", lines)
	}
}

func hasLog(lines []logEntry, level logging.Level, want string) bool {
	for _, e := range lines {
		if e.level == level && strings.Contains(e.line, want) {
			return true
		}
	}
	return false
}
