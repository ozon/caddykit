package model

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

func spec() Spec {
	return Spec{
		Host:        "app.example.com",
		Path:        "/api/*",
		Upstream:    "127.0.0.1:3000",
		StripPrefix: "/api",
		RequestSet:  map[string][]string{"X-Foo": {"bar"}},
		ResponseSet: map[string][]string{"X-A": {"1"}},
	}
}

func TestNormalizationIdempotent(t *testing.T) {
	inputs := []Route{
		spec().Build(),
		Route{"match": []any{}, "handle": []any{map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": "127.0.0.1:1"}}}}},
		Route{"match": []any{map[string]any{"host": []any{"a.example.com"}, "path": []any{}}}, "handle": []any{map[string]any{"handler": "headers", "request": map[string]any{"set": map[string]any{}, "delete": []any{}}}, map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": "h:1"}}}}},
	}
	for i, in := range inputs {
		once := Normalize(in)
		twice := Normalize(once)
		if !Equal(once, twice) {
			t.Fatalf("case %d not idempotent:\n%s\n%s", i, once, twice)
		}
	}
}

func TestExtractBuildRoundtrip(t *testing.T) {
	r := spec().Build()
	s, class, reason := Extract(r)
	if reason != nil || class != FormEditable {
		t.Fatalf("not editable: %+v %+v", class, reason)
	}
	if s.Host != "app.example.com" || s.Upstream != "127.0.0.1:3000" || s.StripPrefix != "/api" || s.Path != "/api/*" {
		t.Fatalf("bad extract: %+v", s)
	}
	rebuilt := s.Build()
	if !Equal(r, rebuilt) {
		t.Fatalf("roundtrip mismatch:\n%s\n%s", r, rebuilt)
	}
}

func TestClassifyJSONOnly(t *testing.T) {
	cases := []Route{
		{"match": []any{map[string]any{"host": []any{"a", "b"}}}, "handle": []any{map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": "h:1"}}}}},
		{"match": []any{map[string]any{"host": []any{"a"}}}, "handle": []any{map[string]any{"handler": "file_server"}}},
		{"match": []any{map[string]any{"host": []any{"a"}, "method": []any{"GET"}}}, "handle": []any{map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": "h:1"}}}}},
		{"match": []any{map[string]any{"host": []any{"a"}}}, "handle": []any{map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": "h:1"}, map[string]any{"dial": "h:2"}}}}},
	}
	for i, r := range cases {
		_, class, reason := Extract(r)
		if class != JSONOnly || reason == nil {
			t.Fatalf("case %d should be JSON_ONLY: %+v %+v", i, class, reason)
		}
	}
}

func TestUnknownTLSFieldsPreserved(t *testing.T) {
	r := Route{
		"match":    []any{map[string]any{"host": []any{"a.example.com"}}},
		"terminal": true,
		"handle": []any{map[string]any{
			"handler":   "reverse_proxy",
			"upstreams": []any{map[string]any{"dial": "h:443"}},
			"transport": map[string]any{"protocol": "http", "tls": map[string]any{"server_name": "x", "custom_ca": "keep"}},
		}},
	}
	s, class, reason := Extract(r)
	if reason != nil || class != FormEditable {
		t.Fatalf("unexpected: %+v %+v", class, reason)
	}
	if s.ExtraTLS["custom_ca"] != "keep" {
		t.Fatalf("extra TLS field lost: %+v", s.ExtraTLS)
	}
	rebuilt := s.Build()
	tr := rebuilt["handle"].([]any)[0].(map[string]any)["transport"].(map[string]any)
	if tr["tls"].(map[string]any)["custom_ca"] != "keep" {
		t.Fatalf("extra TLS field lost in build: %s", rebuilt)
	}
	// Form edits travel through JSON (CLI/UI); unknown TLS fields must survive.
	data, _ := json.Marshal(s)
	var round Spec
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatal(err)
	}
	rebuilt2 := round.Build()
	tr2 := rebuilt2["handle"].([]any)[0].(map[string]any)["transport"].(map[string]any)
	if tr2["tls"].(map[string]any)["custom_ca"] != "keep" {
		t.Fatalf("extra TLS field lost through JSON roundtrip: %s", rebuilt2)
	}
}

func TestHostCollision(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"a.example.com", "a.example.com", true},
		{"a.example.com", "b.example.com", false},
		{"*.example.com", "a.example.com", true},
		{"a.example.com", "*.example.com", true},
		{"*.example.com", "*.example.com", true},
		{"*.a.example.com", "*.example.com", true},
		{"*.example.com", "other.org", false},
		{"*", "anything.org", true},
	}
	for _, c := range cases {
		if got := HostsCollide(c.a, c.b); got != c.want {
			t.Errorf("HostsCollide(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestPathOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"", "/api", true},
		{"/api/*", "/api/users", true},
		{"/api/*", "/api", true},
		{"/api/*", "/apix", false},
		{"/api", "/api/v1", true},
		{"/api", "/apix", false},
		{"/a/*", "/b/*", false},
	}
	for _, c := range cases {
		if got := PathsOverlap(c.a, c.b); got != c.want {
			t.Errorf("PathsOverlap(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestRoutesCollide(t *testing.T) {
	a := Spec{Host: "*.example.com", Path: "/api/*", Upstream: "h:1"}.Build()
	b := Spec{Host: "app.example.com", Path: "/api/users", Upstream: "h:2"}.Build()
	c := Spec{Host: "app.example.com", Path: "/web", Upstream: "h:3"}.Build()
	if !RoutesCollide(a, b) {
		t.Fatal("wildcard/path collision missed")
	}
	if RoutesCollide(b, c) {
		t.Fatal("different paths must not collide")
	}
}

func TestDiffAndRedaction(t *testing.T) {
	old := Spec{Host: "a.example.com", Upstream: "h:1", RequestSet: map[string][]string{"Authorization": {"Bearer old"}}}.Build()
	nw := Spec{Host: "a.example.com", Upstream: "h:2", RequestSet: map[string][]string{"Authorization": {"Bearer new"}}}.Build()
	ch := DiffRoute(&old, &nw, 2)
	if len(ch) == 0 {
		t.Fatal("no diff")
	}
	if ch[0].Path != "/routes/2/handle/0/request/set/Authorization/0" {
		// path depends on handler index; only assert it points at the header
		found := false
		for _, c := range ch {
			if HeaderFieldFromPointer(c.Path) == "Authorization" {
				found = true
			}
		}
		if !found {
			t.Fatalf("header change missing: %+v", ch)
		}
	}
	red := Redact(ch, nil)
	for _, c := range red {
		if HeaderFieldFromPointer(c.Path) == "Authorization" {
			if c.After != "•••" && c.Before != "•••" {
				if s, ok := c.After.([]any); ok && len(s) > 0 && s[0] == "•••" {
					continue
				}
				t.Fatalf("value not redacted: %+v", c)
			}
		}
	}
}

func TestRedactWholeRoute(t *testing.T) {
	sp := Spec{Host: "a.example.com", Upstream: "h:1", RequestSet: map[string][]string{
		"Authorization": {"Bearer super-secret"},
		"X-Trace":       {"keep"},
	}}
	r := sp.Build()
	ch := Redact(DiffRoute(nil, &r, 0), nil)
	b, _ := json.Marshal(ch)
	if strings.Contains(string(b), "super-secret") {
		t.Fatalf("secret leaked in whole-route diff: %s", b)
	}
	if !strings.Contains(string(b), "keep") {
		t.Fatalf("non-sensitive header was dropped: %s", b)
	}
	if !strings.Contains(string(b), "•••") {
		t.Fatalf("no redaction marker: %s", b)
	}
}

func TestTerminalFalseIsJSONOnly(t *testing.T) {
	r := Spec{Host: "a.example.com", Upstream: "h:1"}.Build()
	r["terminal"] = false
	if _, class, reason := Extract(r); class != JSONOnly || reason == nil || reason.Code != RTerminalFalse {
		t.Fatalf("terminal=false must be JSON_ONLY: %v %+v", class, reason)
	}
	delete(r, "terminal")
	if _, class, _ := Extract(r); class != JSONOnly {
		t.Fatal("missing terminal must be JSON_ONLY")
	}
}

func TestDecodePreservesNumbers(t *testing.T) {
	r, err := Decode([]byte(`{"@id":"x","match":[],"handle":[],"terminal":true,"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if string(b) != `{"@id":"x","handle":[],"match":[],"terminal":true,"x":1}` {
		t.Fatalf("unexpected encoding: %s", b)
	}
}

func TestNormalizeIdempotentRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	hosts := []string{"a.example.com", "*.example.com", "b.test"}
	paths := []string{"", "/api/*", "/web"}
	for i := 0; i < 300; i++ {
		h := map[string]any{"host": []any{hosts[rng.Intn(len(hosts))]}}
		if p := paths[rng.Intn(len(paths))]; p != "" {
			h["path"] = []any{p}
		}
		handlers := []any{}
		if rng.Intn(2) == 0 {
			handlers = append(handlers, map[string]any{"handler": "rewrite", "strip_path_prefix": "/api"})
		}
		if rng.Intn(2) == 0 {
			handlers = append(handlers, map[string]any{"handler": "headers",
				"request": map[string]any{"set": map[string]any{"X-A": []any{"1"}}}})
		}
		handlers = append(handlers, map[string]any{
			"handler":   "reverse_proxy",
			"upstreams": []any{map[string]any{"dial": "127.0.0.1:1"}},
		})
		r := Route{"match": []any{h}, "handle": handlers, "terminal": rng.Intn(2) == 0}
		once := Normalize(r)
		twice := Normalize(once)
		if !Equal(once, twice) {
			t.Fatalf("iteration %d: not idempotent\n%s\n%s", i, once, twice)
		}
	}
}

func TestNewIDFormat(t *testing.T) {
	id := NewID()
	if len(id) < len("caddykit/r/")+36 || id[:len("caddykit/r/")] != "caddykit/r/" {
		t.Fatalf("bad id %q", id)
	}
}

func TestValidateJSONRouteNested(t *testing.T) {
	r, err := Decode([]byte(`{
		"match":[{"host":["nested.example.com"]}],
		"handle":[{"handler":"subroute","routes":[
			{"handle":[
				{"handler":"encode","encodings":{"gzip":{}}},
				{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:3000"}]}
			]}
		]}],
		"terminal":true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateJSONRoute(r, ""); err != nil {
		t.Fatal(err)
	}
}

func TestValidateJSONRouteRejectsNestedWithoutHandler(t *testing.T) {
	r, err := Decode([]byte(`{
		"handle":[{"handler":"subroute","routes":[{"handle":[{}]}]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateJSONRoute(r, ""); err == nil {
		t.Fatal("expected an error for a nested handler without name")
	}
}
