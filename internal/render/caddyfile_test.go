package render

import (
	"strings"
	"testing"

	"caddykit/internal/model"
)

func mustRoute(t *testing.T, src string) model.Route {
	t.Helper()
	r, err := model.Decode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRenderSimpleRoute(t *testing.T) {
	out, warnings := Route(mustRoute(t, `{
		"match":[{"host":["app.example.com"]}],
		"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:3000"}]}],
		"terminal":true
	}`))
	if !strings.Contains(out, "app.example.com {") || !strings.Contains(out, "reverse_proxy 127.0.0.1:3000") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
}

func TestRenderNestedHandlers(t *testing.T) {
	out, warnings := Route(mustRoute(t, `{
		"match":[{"host":["ai.example.com"]}],
		"handle":[{"handler":"subroute","routes":[
			{"handle":[
				{"handler":"encode","encodings":{"gzip":{},"zstd":{}}},
				{"handler":"headers","response":{"set":{"X-Frame-Options":["DENY"]}}}
			]},
			{"match":[{"header":{"Upgrade":["websocket"]}}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:20132"}]}]},
			{"match":[{"path":["/v1/*"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:20129"}]}]},
			{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:20128"}]}]}
		]}],
		"terminal":true
	}`))
	for _, want := range []string{
		"ai.example.com {",
		"encode gzip zstd",
		"header X-Frame-Options DENY",
		"Upgrade websocket",
		"127.0.0.1:20132",
		"127.0.0.1:20129",
		"127.0.0.1:20128",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
}

func TestRenderUnknownHandler(t *testing.T) {
	out, warnings := Route(mustRoute(t, `{
		"match":[{"host":["x.example.com"]}],
		"handle":[{"handler":"mystery","foo":"bar"}]
	}`))
	if !strings.Contains(out, "unsupported handler") || len(warnings) == 0 {
		t.Fatalf("expected warning and comment:\n%s\n%v", out, warnings)
	}
}

func TestRenderNoHost(t *testing.T) {
	out, warnings := Route(mustRoute(t, `{
		"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:3000"}]}]
	}`))
	if !strings.Contains(out, ":443 {") || len(warnings) == 0 {
		t.Fatalf("expected :443 fallback and warning:\n%s\n%v", out, warnings)
	}
}

func TestRenderHeaders(t *testing.T) {
	out, warnings := Route(mustRoute(t, `{
		"match":[{"host":["h.example.com"]}],
		"handle":[{"handler":"headers","request":{
			"set":{"X-Req":["one","two"]},
			"delete":["X-Old"]
		},"response":{
			"set":{"X-Frame-Options":["DENY"],"X-Multi":["a","b"]},
			"delete":["X-Powered-By"],
			"deferred":true
		}}],
		"terminal":true
	}`))
	for _, want := range []string{"request_header X-Req one", "request_header +X-Req two", "request_header -X-Old", "X-Frame-Options DENY", "+X-Multi b", "-X-Powered-By", "defer"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
}

func TestRenderDepthLimit(t *testing.T) {
	inner := `{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:1"}]}]}`
	for i := 0; i < 40; i++ {
		inner = `{"handle":[{"handler":"subroute","routes":[` + inner + `]}]}`
	}
	out, warnings := Route(mustRoute(t, `{"match":[{"host":["deep.example.com"]}],"handle":[`+inner+`],"terminal":true}`))
	if len(warnings) == 0 {
		t.Fatalf("expected depth warning:\n%s", out)
	}
}

func TestRenderTerminalFalseWarns(t *testing.T) {
	_, warnings := Route(mustRoute(t, `{
		"match":[{"host":["f.example.com"]}],
		"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:1"}]}]
	}`))
	if len(warnings) == 0 {
		t.Fatal("expected terminal=false warning")
	}
}

func TestRenderEmptyHost(t *testing.T) {
	out, warnings := Route(mustRoute(t, `{
		"match":[{"host":[""]}],
		"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:3000"}]}]
	}`))
	if !strings.Contains(out, ":443 {") || len(warnings) == 0 {
		t.Fatalf("expected :443 fallback and warning:\n%s\n%v", out, warnings)
	}
}
