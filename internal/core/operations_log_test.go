package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"devproxy/internal/model"
)

func TestOperationLogApplyUndoAndFailure(t *testing.T) {
	svc, _ := newTestService(t)
	var lines []string
	svc.Logf = func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	ctx := context.Background()

	p := mustPreview(svc.PreviewCreate(ctx, model.Spec{Host: "log.example.com", Upstream: "127.0.0.1:3000"}, "create log route", nil))
	res, err := svc.Apply(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLog(lines, "apply ok: kind=create route="+res.RouteID) {
		t.Fatalf("apply was not logged; lines: %v", lines)
	}

	lines = nil
	if _, err := svc.Apply(ctx, p); err == nil {
		t.Fatal("second apply of the same preview should fail")
	}
	if !hasLog(lines, "apply failed:") || !hasLog(lines, "preview is stale") {
		t.Fatalf("apply failure was not logged with its cause; lines: %v", lines)
	}

	lines = nil
	undoRes, err := svc.Undo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLog(lines, "undo ok: kind=undo route="+undoRes.RouteID) {
		t.Fatalf("undo was not logged; lines: %v", lines)
	}
}

func hasLog(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}
