//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"caddykit/internal/model"
	"caddykit/internal/state"
)

func TestSequence(t *testing.T) {
	box := startCaddy(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "yes")
		_, _ = io.WriteString(w, "upstream-ok")
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	upstreamHost := u.Host

	dataDir := t.TempDir()
	web := startWeb(t, box, "it.test", dataDir)
	defer func() {
		if web.cmd.ProcessState == nil {
			signalWeb(web, syscall.SIGTERM)
		}
	}()

	a := &apiClient{t: t, base: web.base}
	// Wrong password first: one failure must not block the next attempt.
	if status, _, _ := a.request("POST", "/api/v1/login", map[string]string{"password": "wrong"}, false); status != 401 {
		t.Fatalf("wrong password: %d", status)
	}
	a.login(web.password)

	// Unauthenticated access is rejected.
	anon := &apiClient{t: t, base: web.base}
	if status, _, _ := anon.request("GET", "/api/v1/routes", nil, false); status != 401 {
		t.Fatalf("anonymous routes: %d", status)
	}

	// CSRF is mandatory for writes.
	if status, body, _ := a.request("POST", "/api/v1/routes/preview", map[string]any{"kind": "create"}, false); status != 403 {
		t.Fatalf("missing CSRF: %d %s", status, body)
	}

	// Create.
	p := a.preview("create", map[string]any{"spec": map[string]any{
		"host": "app.it.test", "upstream": upstreamHost, "path": "/api/*", "strip_prefix": "/api",
		"request_set": map[string][]string{"X-Auth": {"secret-value"}},
	}})
	id, _ := p["route_id"].(string)
	if id == "" {
		t.Fatal("no route id in preview")
	}
	a.apply(p)
	if box.findRoute(box.routes(t), id) == nil {
		t.Fatal("route not written to Caddy")
	}

	// Full route JSON: nested subroute with multiple handlers, plus a
	// read-only Caddyfile preview that Caddy itself can adapt.
	nested := map[string]any{
		"match": []any{map[string]any{"host": []any{"nested.it.test"}}},
		"handle": []any{map[string]any{"handler": "subroute", "routes": []any{
			map[string]any{"handle": []any{
				map[string]any{"handler": "encode", "encodings": map[string]any{"gzip": map[string]any{}}},
				map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": upstreamHost}}},
			}},
		}}},
		"terminal": true,
	}
	np := a.preview("create", map[string]any{"route": nested})
	nid, _ := np["route_id"].(string)
	if nid == "" {
		t.Fatalf("nested create preview has no route id: %+v", np)
	}
	a.apply(np)
	if box.findRoute(box.routes(t), nid) == nil {
		t.Fatal("nested route not applied")
	}

	var preview map[string]any
	if status := a.json("POST", "/api/v1/render/caddyfile", map[string]any{"route": nested}, &preview); status != 200 {
		t.Fatalf("render caddyfile: %d %+v", status, preview)
	}
	text, _ := preview["caddyfile"].(string)
	resp, err := http.Post(box.admin+"/adapt", "text/caddyfile", strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	adapted, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || strings.Contains(string(adapted), `"error"`) {
		t.Fatalf("generated caddyfile does not adapt: %d %s\n%s", resp.StatusCode, adapted, text)
	}

	// Proxy through Caddy (Host header match).
	waitFor(t, "proxy works", 10*time.Second, func() bool {
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/hello", box.http), nil)
		req.Host = "app.it.test"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode == 200 && string(body) == "upstream-ok"
	})

	// CLI commands use the same state owner through the IPC socket.
	cliOut, err := web.cli(t, dataDir, "routes", "list",
		"--data-dir", dataDir, "--caddy-admin", box.admin, "--server", "srv0")
	if err != nil || !strings.Contains(cliOut, "app.it.test") {
		t.Fatalf("CLI list via IPC failed: %v\n%s", err, cliOut)
	}
	cliOut, err = web.cli(t, dataDir, "routes", "add",
		"--data-dir", dataDir, "--caddy-admin", box.admin, "--server", "srv0",
		"--domain", "cli.it.test", "--upstream", upstreamHost, "--yes")
	if err != nil {
		t.Fatalf("CLI add via IPC failed: %v\n%s", err, cliOut)
	}
	if findHost(box.routes(t), "cli.it.test") == nil {
		t.Fatal("CLI add did not reach Caddy")
	}

	// Undo must not be touched by an automatic restore.
	undoBefore := getUndo(t, a)

	// External delete -> restore.
	var routes []map[string]any
	for _, r := range box.routes(t) {
		if r["@id"] != id {
			routes = append(routes, r)
		}
	}
	box.setRoutes(t, routes)
	waitFor(t, "restore after external delete", 15*time.Second, func() bool {
		return box.findRoute(box.routes(t), id) != nil
	})
	if undoAfter := getUndo(t, a); undoAfter["description"] != undoBefore["description"] {
		t.Fatalf("restore touched undo: %v -> %v", undoBefore, undoAfter)
	}

	// External modify -> conflict.
	modifyRoute(t, box, id, func(r map[string]any) {
		handlers := r["handle"].([]any)
		for _, h := range handlers {
			hm := h.(map[string]any)
			if hm["handler"] == "reverse_proxy" {
				hm["upstreams"] = []any{map[string]any{"dial": "127.0.0.1:1"}}
			}
		}
	})
	waitConflict(t, a, id, "EXTERNALLY_MODIFIED")

	// Resolution 1: take-current.
	resolve(t, a, id, "take-current")
	if cs := getConflicts(t, a); len(cs) != 0 {
		t.Fatalf("take-current left conflicts: %+v", cs)
	}

	// Modify again, resolution 2: apply-desired.
	modifyRoute(t, box, id, func(r map[string]any) {
		handlers := r["handle"].([]any)
		for _, h := range handlers {
			hm := h.(map[string]any)
			if hm["handler"] == "reverse_proxy" {
				hm["upstreams"] = []any{map[string]any{"dial": "127.0.0.1:2"}}
			}
		}
	})
	waitConflict(t, a, id, "EXTERNALLY_MODIFIED")
	resolve(t, a, id, "apply-desired")
	if got := routeDial(t, box, id); got != "127.0.0.1:1" {
		t.Fatalf("apply-desired did not restore the taken-current dial: %s", got)
	}

	// Modify again, resolution 3: unmanage leaves Caddy unchanged.
	modifyRoute(t, box, id, func(r map[string]any) {
		handlers := r["handle"].([]any)
		for _, h := range handlers {
			hm := h.(map[string]any)
			if hm["handler"] == "reverse_proxy" {
				hm["upstreams"] = []any{map[string]any{"dial": "127.0.0.1:3"}}
			}
		}
	})
	waitConflict(t, a, id, "EXTERNALLY_MODIFIED")
	resolve(t, a, id, "unmanage")
	if routeDial(t, box, id) != "127.0.0.1:3" {
		t.Fatal("unmanage changed Caddy")
	}
	// The route is now foreign and can be adopted again.
	ap := a.preview("adopt", map[string]any{"id": id})
	a.apply(ap)

	// Create route number 2 for the undo-across-restart test.
	p2 := a.preview("create", map[string]any{"spec": map[string]any{
		"host": "undo.it.test", "upstream": upstreamHost,
	}})
	id2 := p2["route_id"].(string)
	a.apply(p2)
	if box.findRoute(box.routes(t), id2) == nil {
		t.Fatal("undo route missing")
	}

	// Graceful shutdown: self route becomes static 503, dev route stays.
	stopWeb(t, web)
	// Without the web process the CLI falls back to a one-shot under locks.
	cliOut, err = web.cli(t, dataDir, "routes", "list",
		"--data-dir", dataDir, "--caddy-admin", box.admin, "--server", "srv0")
	if err != nil || !strings.Contains(cliOut, "app.it.test") {
		t.Fatalf("one-shot CLI failed: %v\n%s", err, cliOut)
	}
	self := box.selfRoute(t)
	if self == nil {
		t.Fatal("self route disappeared on shutdown")
	}
	h := self["handle"].([]any)[0].(map[string]any)
	if h["handler"] != "static_response" || fmt.Sprint(h["status_code"]) != "503" {
		t.Fatalf("self route not 503: %+v", self)
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", box.http), nil)
	req.Host = "undo.it.test"
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("dev route not active after shutdown: %v %v", err, resp)
	}
	resp.Body.Close()

	// Restart: session from the old process is dead, self route is repaired.
	web2 := startWeb(t, box, "it.test", dataDir)
	defer func() { signalWeb(web2, syscall.SIGTERM) }()
	old := &apiClient{t: t, base: web2.base, session: a.session, csrf: a.csrf}
	if status, _, _ := old.request("GET", "/api/v1/routes", nil, false); status != 401 {
		t.Fatalf("old session survived restart: %d", status)
	}
	a2 := &apiClient{t: t, base: web2.base}
	a2.login(web2.password)
	self2 := box.selfRoute(t)
	h2 := self2["handle"].([]any)[0].(map[string]any)
	if h2["handler"] != "reverse_proxy" {
		t.Fatalf("self route not repaired: %+v", self2)
	}

	// Undo survives the restart.
	var undoRes map[string]any
	if status := a2.json("POST", "/api/v1/undo", map[string]any{}, &undoRes); status != 200 {
		t.Fatalf("undo after restart: %d %+v", status, undoRes)
	}
	if box.findRoute(box.routes(t), id2) != nil {
		t.Fatal("undo did not remove the route created before restart")
	}

	// Second web process is refused with exit code 6.
	second := exec.Command(caddykitBinary(t), "web", "--domain", "it.test", "--caddy-admin", box.admin,
		"--server", "srv0", "--data-dir", dataDir, "--reconcile-interval", "2s")
	second.Env = append(second.Environ(), "XDG_RUNTIME_DIR="+t.TempDir())
	out, err := second.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 6 {
		t.Fatalf("second instance: err=%v out=%s", err, out)
	}
	stopWeb(t, web2)

	// Without a web process there is no background reconciliation: an
	// externally deleted managed route stays deleted and a one-shot CLI call
	// must not restore it (criterion 19).
	var cliRoute map[string]any
	for _, r := range box.routes(t) {
		cliRoute = findHost([]map[string]any{r}, "cli.it.test")
		if cliRoute != nil {
			break
		}
	}
	if cliRoute == nil {
		t.Fatal("CLI route missing")
	}
	cliID := cliRoute["@id"].(string)
	var remaining []map[string]any
	for _, r := range box.routes(t) {
		if r["@id"] != cliID {
			remaining = append(remaining, r)
		}
	}
	box.setRoutes(t, remaining)
	cliOut, err = web2.cli(t, dataDir, "routes", "list",
		"--data-dir", dataDir, "--caddy-admin", box.admin, "--server", "srv0")
	if err != nil || !strings.Contains(cliOut, "cli.it.test") {
		t.Fatalf("one-shot list after delete: %v\n%s", err, cliOut)
	}
	time.Sleep(3 * time.Second)
	if findHost(box.routes(t), "cli.it.test") != nil {
		t.Fatal("one-shot CLI restored a route without a web process")
	}
}

func getUndo(t *testing.T, a *apiClient) map[string]any {
	t.Helper()
	var u map[string]any
	if s := a.json("GET", "/api/v1/undo", nil, &u); s != 200 {
		t.Fatalf("undo info: %d", s)
	}
	return u
}

func getConflicts(t *testing.T, a *apiClient) []map[string]any {
	t.Helper()
	var out struct {
		Conflicts []map[string]any `json:"conflicts"`
	}
	if s := a.json("GET", "/api/v1/conflicts", nil, &out); s != 200 {
		t.Fatalf("conflicts: %d", s)
	}
	return out.Conflicts
}

func waitConflict(t *testing.T, a *apiClient, id, kind string) {
	t.Helper()
	waitFor(t, "conflict "+kind, 15*time.Second, func() bool {
		for _, c := range getConflicts(t, a) {
			if c["route_id"] == id && c["kind"] == kind {
				return true
			}
		}
		return false
	})
}

func resolve(t *testing.T, a *apiClient, id, action string) {
	t.Helper()
	var p map[string]any
	if s := a.json("POST", "/api/v1/conflicts/resolve", map[string]string{"id": id, "action": action}, &p); s != 200 {
		t.Fatalf("resolve %s: %d %+v", action, s, p)
	}
	if b, ok := p["blocked"].(map[string]any); ok && b != nil {
		t.Fatalf("resolve %s blocked: %+v", action, b)
	}
	a.apply(p)
}

func modifyRoute(t *testing.T, box *caddyBox, id string, fn func(map[string]any)) {
	t.Helper()
	routes := box.routes(t)
	for _, r := range routes {
		if r["@id"] == id {
			fn(r)
		}
	}
	box.setRoutes(t, routes)
}

func findHost(routes []map[string]any, host string) map[string]any {
	for _, r := range routes {
		m, _ := r["match"].([]any)
		if len(m) == 0 {
			continue
		}
		hosts, _ := m[0].(map[string]any)["host"].([]any)
		for _, h := range hosts {
			if h == host {
				return r
			}
		}
	}
	return nil
}

func routeDial(t *testing.T, box *caddyBox, id string) string {
	t.Helper()
	r := box.findRoute(box.routes(t), id)
	if r == nil {
		return ""
	}
	for _, h := range r["handle"].([]any) {
		hm := h.(map[string]any)
		if hm["handler"] == "reverse_proxy" {
			ups := hm["upstreams"].([]any)
			return ups[0].(map[string]any)["dial"].(string)
		}
	}
	return ""
}

func TestSecurityAndStartup(t *testing.T) {
	t.Run("remote admin guard", func(t *testing.T) {
		dir := t.TempDir()
		cmd := exec.Command(caddykitBinary(t), "routes", "list",
			"--caddy-admin", "http://10.255.255.1:2019", "--data-dir", dir)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 2 {
			t.Fatalf("remote admin must exit 2: err=%v out=%s", err, out)
		}
		cmd = exec.Command(caddykitBinary(t), "routes", "list", "--allow-remote-admin",
			"--caddy-admin", "http://10.255.255.1:2019", "--data-dir", dir)
		out, err = cmd.CombinedOutput()
		exitErr, ok = err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 2 {
			t.Fatalf("plaintext remote admin must exit 2 even with the flag: err=%v out=%s", err, out)
		}
	})

	t.Run("missing etag refuses start", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		cmd := exec.Command(caddykitBinary(t), "web", "--domain", "etag.test",
			"--caddy-admin", srv.URL, "--data-dir", t.TempDir(), "--reconcile-interval", "2s")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("start must fail without Etag: %s", out)
		}
		if !contains(string(out), "Etag") && !contains(string(out), "2.8") {
			t.Fatalf("unhelpful error: %s", out)
		}
	})

	t.Run("self domain is not overwritten", func(t *testing.T) {
		box := startCaddy(t)
		foreign := map[string]any{
			"match":    []any{map[string]any{"host": []any{"occupied.test"}}},
			"handle":   []any{map[string]any{"handler": "static_response", "body": "foreign"}},
			"terminal": true,
		}
		box.setRoutes(t, []map[string]any{foreign})
		cmd := exec.Command(caddykitBinary(t), "web", "--domain", "occupied.test",
			"--caddy-admin", box.admin, "--server", "srv0",
			"--data-dir", t.TempDir(), "--reconcile-interval", "2s")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("start must fail on a foreign domain: %s", out)
		}
		if !contains(string(out), "already used") {
			t.Fatalf("unhelpful error: %s", out)
		}
		routes := box.routes(t)
		if len(routes) != 1 || routes[0]["@id"] != nil {
			t.Fatalf("foreign config was modified: %+v", routes)
		}
	})
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestUnclearJournalStartup(t *testing.T) {
	box := startCaddy(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)

	dataDir := t.TempDir()
	web := startWeb(t, box, "unclear.test", dataDir)
	a := &apiClient{t: t, base: web.base}
	a.login(web.password)
	p := a.preview("create", map[string]any{"spec": map[string]any{
		"host": "unclear-app.test", "upstream": u.Host,
	}})
	id := p["route_id"].(string)
	a.apply(p)
	stopWeb(t, web)

	// Simulate a crash whose Caddy state matches neither the before nor the
	// after image.
	store, err := state.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	oldRoute := model.Spec{Host: "ghost-old.test", Upstream: "127.0.0.1:1"}.Build()
	newRoute := model.Spec{Host: "ghost-new.test", Upstream: "127.0.0.1:2"}.Build()
	if err := store.SaveJournal(&state.Journal{
		OpID: "it-unclear", Kind: "edit", RouteID: id, Phase: state.PhasePrepared,
		Old:       &state.Image{Route: oldRoute, Index: 0},
		New:       &state.Image{Route: newRoute, Index: 0},
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	web2 := startWeb(t, box, "unclear.test", dataDir)
	defer func() { signalWeb(web2, syscall.SIGTERM) }()
	a2 := &apiClient{t: t, base: web2.base}
	a2.login(web2.password)
	found := false
	for _, c := range getConflicts(t, a2) {
		if c["route_id"] == id && c["kind"] == "OP_UNCLEAR" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unclear journal not surfaced as OP_UNCLEAR: %+v", getConflicts(t, a2))
	}
	// Startup continued, so the user can resolve it.
	resolve(t, a2, id, "unmanage")
	if cs := getConflicts(t, a2); len(cs) != 0 {
		t.Fatalf("unclear conflict not resolved: %+v", cs)
	}
	stopWeb(t, web2)
}
