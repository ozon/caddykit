package core

import (
	"context"
	"strings"
	"testing"

	"caddykit/internal/model"
)

func TestAdoptPreviewCarriesDiff(t *testing.T) {
	svc, f := newTestService(t)
	f.set([]model.Route{{"match": []any{map[string]any{"host": []any{"foreign.example.com"}}}, "terminal": true}})
	ctx := context.Background()

	p, err := svc.PreviewAdopt(ctx, "foreign.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if p.Blocked != nil {
		t.Fatalf("unexpected block: %+v", p.Blocked)
	}
	if p.Diff == nil {
		t.Fatal("adopt preview has a nil diff")
	}
	found := false
	for _, c := range p.Diff {
		if c.Op == "add" && strings.HasSuffix(c.Path, "/@id") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diff does not report the assigned @id: %+v", p.Diff)
	}
	if p.Route == nil || (*p.Route)["@id"] == nil {
		t.Fatal("adopted route has no @id")
	}
	if p.Base != nil {
		if _, mutated := (*p.Base)["@id"]; mutated {
			t.Fatal("base route was mutated by the adoption")
		}
	}
}
