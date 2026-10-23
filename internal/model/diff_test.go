package model

import "testing"

func TestDiffRouteEmptyIsNotNil(t *testing.T) {
	r := Route{"match": []any{map[string]any{"host": []any{"a.example.com"}}}}
	d := DiffRoute(&r, &r, 0)
	if d == nil {
		t.Fatal("unchanged route produced a nil diff; JSON would be null")
	}
	if len(d) != 0 {
		t.Fatalf("unchanged route produced %d changes", len(d))
	}
	if nilDiff := DiffRoute(nil, nil, 0); nilDiff == nil {
		t.Fatal("nil/nil route produced a nil diff")
	}
}
