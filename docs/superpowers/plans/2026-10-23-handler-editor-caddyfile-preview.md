# Handler-Editor und Caddyfile-Preview — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (native) or superpowers:subagent-driven-development. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Bestehende Caddy-Routen mit beliebig verschachtelten Handlern im UI strukturiert editierbar machen und eine read-only Caddyfile-Vorschau anbieten.

**Architecture:** Der Form-Tab wird ein rekursiver, JSON-basierter Struktur-Editor. Das Route-JSON bleibt die einzige Datenquelle; bekannte Handler bekommen typisierte Felder, unbekannte Felder einen generischen Editor. Ein neues Go-Paket rendert Routen best-effort als Caddyfile, nur zur Anzeige.

**Tech Stack:** Go 1.23, Caddy Admin API, SvelteKit 3 / Svelte 5, Tailwind 4, CodeMirror 6 (nur JSON-Tab).

**Spec:** `docs/superpowers/specs/2026-10-23-handler-editor-caddyfile-preview-design.md`

## Global Constraints

- Kein Caddyfile-Dateizugriff, kein Caddyfile-Import, kein Apply eines Caddyfiles.
- Caddyfile-Ausgabe ist eine Näherung und wird nie an Caddy gesendet.
- Keine neuen Frontend-Abhängigkeiten; Bundle < 400 KB gzip.
- Routen-JSON bleibt vollständig erhalten; unbekannte Felder dürfen nicht verloren gehen.
- Fremde Routen werden beim Speichern adoptiert; `devproxy/self` bleibt gesperrt.
- `insecure_skip_verify` bleibt hinter `--allow-insecure`.
- Bestehende Sicherheitsmechanismen (0600/0700, CSRF, Etag/If-Match, Auth) bleiben unverändert.
- Kein Git-Repository: Commit-Schritte entfallen; jeder Task endet mit den genannten Checks.

## Review Focus

- Tief verschachtelte oder pathologische JSON-Bäume dürfen den Renderer nicht sprengen (Tiefenlimit).
- Unbekannte Handler-/Matcher-Felder müssen beim Speichern erhalten bleiben.
- Mehrere Handler in einem `route`/`subroute` müssen ihre Reihenfolge behalten.
- Leere Handler, fehlende Hosts und leere `handle`-Arrays dürfen weder Panik noch kaputte Vorschauen erzeugen.
- Header-Werte können Secrets enthalten; die Caddyfile-Vorschau braucht einen sichtbaren Warnhinweis.
- Ein Speichern einer fremden Route adoptiert sie; das muss in der UI erkennbar bleiben.
- Typisierte Felder und Advanced-JSON müssen denselben Zustand bearbeiten.

---

### Task 1: Rekursive Routen-Validierung und Create aus vollständigem JSON

**Files:**
- Modify: `internal/model/route.go`
- Test: `internal/model/model_test.go`
- Modify: `internal/core/preview.go`
- Test: `internal/core/core_test.go`

**Interfaces:**
- Consumes: `model.Decode`, `model.ValidateJSONRoute`, `model.Route`, `state.Entry`, `Service.readServer`, `collideWith`.
- Produces: `func (s *Service) PreviewCreateJSON(ctx context.Context, raw model.Route, source string) (*Preview, error)`; `ValidateJSONRoute` validates nested `subroute.routes`.

- [x] **Step 1: Write the failing model test**

```go
func TestValidateJSONRouteNested(t *testing.T) {
	r, err := model.Decode([]byte(`{
		"match":[{"host":["nested.example.com"]}],
		"handle":[{"handler":"subroute","routes":[
			{"handle":[
				{"handler":"encode","encodings":{"gzip":{}}},
				{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:3000"}]}
			]}
		]}],
		"terminal":true
	}`))
	if err != nil { t.Fatal(err) }
	if err := model.ValidateJSONRoute(r, ""); err != nil { t.Fatal(err) }
}

func TestValidateJSONRouteRejectsNestedWithoutHandler(t *testing.T) {
	r, err := model.Decode([]byte(`{
		"handle":[{"handler":"subroute","routes":[{"handle":[{}]}]}]
	}`))
	if err != nil { t.Fatal(err) }
	if err := model.ValidateJSONRoute(r, ""); err == nil {
		t.Fatal("expected an error for a nested handler without name")
	}
}
```

- [x] **Step 2: Run the test and verify it fails**

Run: `go test ./internal/model -run TestValidateJSONRoute -v`
Expected: FAIL because the nested handler is accepted.

- [x] **Step 3: Implement recursive validation**

Refactor `ValidateJSONRoute` into a private `validateJSONRoute(r Route, expectedID string, depth int) error`. For every `handle` entry call `validateHandler`; for `handler == "subroute"`, require `routes` to be an array and recurse into each object with `depth+1`. Use `maxRouteDepth = 50`; return an error at the limit. Error messages include the JSON path, e.g. `handle[0].routes[1].handle[0] has no handler`.

- [x] **Step 4: Run the model tests**

Run: `go test ./internal/model -v`
Expected: PASS.

- [x] **Step 5: Write the failing core test**

```go
func TestPreviewCreateJSON(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	raw, err := model.Decode([]byte(`{
		"match":[{"host":["nested.example.com"]}],
		"handle":[{"handler":"subroute","routes":[
			{"handle":[
				{"handler":"encode","encodings":{"gzip":{}}},
				{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:3000"}]}
			]}
		]}],
		"terminal":true
	}`))
	if err != nil { t.Fatal(err) }
	p := mustPreview(svc.PreviewCreateJSON(ctx, raw, "create"))
	if p.Route == nil || p.Entry == nil || p.Route.ID() == "" {
		t.Fatalf("incomplete preview: %+v", p)
	}
	res, err := svc.Apply(ctx, p)
	if err != nil { t.Fatal(err) }
	routes := f.get()
	if len(routes) != 1 || routes[0].ID() != res.RouteID {
		t.Fatalf("route not created: %+v", routes)
	}
	h := routes[0]["handle"].([]any)[0].(map[string]any)
	if h["handler"] != "subroute" {
		t.Fatalf("nested handler lost: %+v", h)
	}
}
```

- [x] **Step 6: Run the core test and verify it fails**

Run: `go test ./internal/core -run TestPreviewCreateJSON -v`
Expected: FAIL because `PreviewCreateJSON` does not exist.

- [x] **Step 7: Implement `PreviewCreateJSON`**

In `preview.go`, lock the service, clone the raw route, overwrite `@id` with `model.NewID()`, call `ValidateJSONRoute`, enforce the insecure gate, read the server, check duplicate IDs and `collideWith`, then build a `Preview` with `KindCreate`, `Route`, `Entry` (`Source: "create"`), `Diff`, `ETag`, `Index: 0`. Description uses `describeRoute`.

- [x] **Step 8: Run the core tests**

Run: `go test ./internal/core -v`
Expected: PASS.

---

### Task 2: Caddyfile-Renderer

**Files:**
- Create: `internal/render/caddyfile.go`
- Test: `internal/render/caddyfile_test.go`

**Interfaces:**
- Consumes: `model.Route`, `model.Decode`.
- Produces: `func Route(r model.Route) (string, []string)`; warnings are human-readable.

- [x] **Step 1: Write the failing renderer tests**

```go
func mustRoute(t *testing.T, src string) model.Route {
	t.Helper()
	r, err := model.Decode([]byte(src))
	if err != nil { t.Fatal(err) }
	return r
}

func TestRenderSimpleRoute(t *testing.T) {
	out, warnings := render.Route(mustRoute(t, `{
		"match":[{"host":["app.example.com"]}],
		"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:3000"}]}],
		"terminal":true
	}`))
	if !strings.Contains(out, "app.example.com {") || !strings.Contains(out, "reverse_proxy 127.0.0.1:3000") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if len(warnings) != 0 { t.Fatalf("warnings: %v", warnings) }
}

func TestRenderNestedHandlers(t *testing.T) {
	out, warnings := render.Route(mustRoute(t, `{
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
	for _, want := range []string{"ai.example.com {", "encode gzip zstd", "header X-Frame-Options DENY", "Upgrade websocket", "127.0.0.1:20132", "127.0.0.1:20129", "127.0.0.1:20128"} {
		if !strings.Contains(out, want) { t.Fatalf("missing %q in:\n%s", want, out) }
	}
	if len(warnings) != 0 { t.Fatalf("warnings: %v", warnings) }
}

func TestRenderUnknownHandler(t *testing.T) {
	out, warnings := render.Route(mustRoute(t, `{
		"match":[{"host":["x.example.com"]}],
		"handle":[{"handler":"mystery","foo":"bar"}]
	}`))
	if !strings.Contains(out, "unsupported handler") || len(warnings) == 0 {
		t.Fatalf("expected warning and comment:\n%s\n%v", out, warnings)
	}
}
```

- [x] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/render -v`
Expected: FAIL because the package does not exist.

- [x] **Step 3: Implement the renderer**

Create `render.Route`. Use a `renderer` struct with `warnings []string`, `matcherN int`, and `maxDepth = 32`. Render a site block from `r.Hosts()`; if no host use `:443` and warn. Render handlers in order: wrap multi-handler groups in `route { ... }`; render `subroute` by recursing into its routes; render `path`/`header` matchers as named `@mN` matchers plus `route @mN { ... }`. Implement handlers:
- `reverse_proxy`: upstreams, optional `flush_interval`, `transport http { tls; tls_server_name ...; tls_insecure_skip_verify }`.
- `headers`: response set (`header Name value`, subsequent values `header +Name value`, deferred in a `header { defer ... }` block), response delete (`header -Name`), request set/delete (`request_header ...`).
- `rewrite`: `uri strip_prefix`; warn about other fields.
- `encode`: `encode gzip zstd` plus `minimum_length` in a block.
- `static_response`: `respond "body" status`.
- Unknown: comment `# unsupported handler "<name>": <json>` plus warning.
Quote values with a token helper; dedupe warnings; stop at maxDepth with a warning.

- [x] **Step 4: Run the renderer tests**

Run: `go test ./internal/render -v`
Expected: PASS.

---

### Task 3: Render-API und Create-Route im Web-API

**Files:**
- Modify: `internal/web/api.go`
- Modify: `test/integration/sequence_test.go`

**Interfaces:**
- Consumes: `core.PreviewCreateJSON`, `render.Route`, `model.Decode`.
- Produces: `POST /api/v1/render/caddyfile` with `{route}` → `{caddyfile, warnings}`; `POST /api/v1/routes/preview` accepts `kind: "create"` plus `route`.

- [x] **Step 1: Add the API handler**

In `api()`, register:

```go
mux.HandleFunc("POST /api/v1/render/caddyfile", s.guard(false, s.renderCaddyfile))
```

Implement `renderCaddyfile` with a `json.RawMessage` route, decode via `model.Decode`, call `render.Route`, and return `{"caddyfile": text, "warnings": warnings}`. Return 400 on invalid input.

- [x] **Step 2: Accept `route` in create previews**

In `preview`, for `core.KindCreate`, check `len(req.Route) > 0 && string(req.Route) != "null"` before template/spec handling; decode and call `s.svc.PreviewCreateJSON(ctx, raw, "create")`.

- [x] **Step 3: Add the integration test**

Extend `TestSequence` after login with a nested route:

```go
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
if nid == "" { t.Fatal("nested create preview has no route id") }
a.apply(np)
if box.findRoute(box.routes(t), nid) == nil { t.Fatal("nested route not applied") }

var caddyfile map[string]any
if status := a.json("POST", "/api/v1/render/caddyfile", map[string]any{"route": nested}, &caddyfile); status != 200 {
	t.Fatalf("render caddyfile: %d", status)
}
text, _ := caddyfile["caddyfile"].(string)
resp, err := http.Post(box.admin+"/adapt", "text/caddyfile", strings.NewReader(text))
if err != nil { t.Fatal(err) }
defer resp.Body.Close()
adapted, _ := io.ReadAll(resp.Body)
if resp.StatusCode != 200 || strings.Contains(string(adapted), `"error"`) {
	t.Fatalf("generated caddyfile does not adapt: %d %s\n%s", resp.StatusCode, adapted, text)
}
```

- [x] **Step 4: Run unit tests and integration**

Run: `go test ./internal/...`
Expected: PASS.
Run: `make integration`
Expected: PASS.

---

### Task 4: Frontend-Helfer und generischer Struktur-Editor

**Files:**
- Modify: `webui/src/lib/types.ts`
- Modify: `webui/src/lib/api.ts`
- Create: `webui/src/lib/route-edit.ts`
- Create: `webui/src/lib/components/GenericFields.svelte`
- Create: `webui/src/lib/components/CaddyfilePreview.svelte`

**Interfaces:**
- `renderCaddyfile(route: unknown): Promise<{ caddyfile: string; warnings: string[] }>`
- `defaultRoute(): any`, `defaultNestedRoute(): any`, `newHandler(kind: string): any`, `validateRoute(route: any): string[]`.
- `GenericFields` props: `{ value: any; exclude?: string[]; depth?: number }`; mutates the passed object in place.
- `CaddyfilePreview` props: `{ route: any }`; debounced fetch, warnings, copy button.

- [x] **Step 1: Add types and API client**

Add `CaddyfilePreview` to `types.ts`; add `renderCaddyfile` to `api.ts` using `POST /api/v1/render/caddyfile` with `{ route }`.

- [x] **Step 2: Implement route-edit helpers**

`defaultRoute` returns `{ match:[{host:[""]}], handle:[{handler:"reverse_proxy", upstreams:[{dial:""}]}], terminal:true }`. `newHandler` returns the presets for the known types. `validateRoute` walks `handle`, checks each handler name, `reverse_proxy` upstreams, `headers` names (no CR/LF), `subroute.routes` recursively with depth 50, and returns paths like `handle[0].upstreams[0]`.

- [x] **Step 3: Implement GenericFields and CaddyfilePreview**

`GenericFields` renders objects/arrays recursively, string/number/boolean controls, add/remove/reorder, and key rename. `CaddyfilePreview` calls `renderCaddyfile` with a 300 ms debounce, cancels stale results with a sequence number, and renders a prominent approximation warning plus the text in a `<pre>`.

- [x] **Step 4: Run the frontend checks**

Run: `cd webui && npm run check`
Expected: 0 errors, 0 warnings.

---

### Task 5: Rekursiver RouteEditor

**Files:**
- Create: `webui/src/lib/components/RouteEditor.svelte`
- Create: `webui/src/lib/components/HeaderFields.svelte`

**Interfaces:**
- `RouteEditor` props: `{ route: any; depth?: number; readonly?: boolean }`.
- Consumes: `GenericFields`, `route-edit` helpers, `route.ts` header helpers.

- [x] **Step 1: Implement the match editor**

Render host and path as comma-separated inputs; header matchers as `Name=Value` lines; an "Advanced match" `GenericFields` excluding `host`, `path`, `header`; and a "No matcher" add button.

- [x] **Step 2: Implement the handler list**

Render each `route.handle` entry as a card with a type select, up/down/delete, and typed fields:
- `reverse_proxy`: upstream dial list, TLS, server name, insecure, flush_interval; extra fields via `GenericFields`.
- `headers`: request/response set/delete via `HeaderFields`; deferred checkbox; extra fields via `GenericFields`.
- `rewrite`: strip prefix; extra fields.
- `encode`: gzip/zstd checkboxes, minimum_length; extra fields.
- `subroute`: nested `routes` list rendered recursively with the same component; add/remove/reorder.
- `static_response`: status/body; extra fields.
- unknown: `GenericFields` over the whole handler.
Provide an add menu with the presets from `newHandler`, plus "Custom JSON".

- [x] **Step 3: Add advanced fields per handler**

Each handler card gets a collapsible "More … fields" `GenericFields` tree for
unknown keys. The route-level JSON tab remains the raw, lossless escape hatch;
a second per-handler JSON textarea would duplicate that editor.

- [x] **Step 4: Run the frontend checks**

Run: `cd webui && npm run check`
Expected: 0 errors.

---

### Task 6: Detailseite, Preview-Dialog und Create-Flows

**Files:**
- Modify: `webui/src/routes/routes/[...id]/+page.svelte`
- Modify: `webui/src/lib/components/PreviewDialog.svelte`
- Modify: `webui/src/routes/routes/new/+page.svelte`
- Create: `webui/src/routes/routes/new/advanced/+page.svelte`
- Modify: `webui/src/lib/components/CommandPalette.svelte`

**Interfaces:**
- Detail page stores `let draft = $state<any>(null)`; form edits send `{ kind: "edit", id, route: draft }`.
- Create-advanced sends `{ kind: "create", route: draft }`.

- [x] **Step 1: Replace the detail form**

Load the route into `draft`. Render `RouteEditor` for non-missing routes, including read-only for `self`. Keep the self/missing/conflict notices. Show `validateRoute(draft)` errors above the submit button. Submit via `openPreview("edit", { id: view.id || id, route: draft })`. Keep the JSON tab; add a Caddyfile tab with `CaddyfilePreview route={draft}`. Remove the old `Spec`-only form and the "not form-editable" block.

- [x] **Step 2: Add the PreviewDialog toggle**

Add a `Diff / Caddyfile` toggle. For `preview.route`, render `CaddyfilePreview`; for delete/unmanage without a route, show "This operation removes the route." Keep the Apply button and blocked/warnings behavior.

- [x] **Step 3: Add the quick-create Caddyfile preview**

In the new-route live preview panel, add a `Diff / Caddyfile` toggle using `previewData.route`. Keep the existing form and add a link/button to `/routes/new/advanced`.

- [x] **Step 4: Implement the advanced create page**

Use `defaultRoute()` in `$state`, render `RouteEditor` and `CaddyfilePreview`, show validation errors, debounce a `preview("create", { route: draft })`, and on apply `goto(routeHref(res.route_id))`. Add a command-palette entry "New advanced route".

- [x] **Step 5: Run the frontend checks and build**

Run: `cd webui && npm run check && npm run lint && npm run build`
Expected: PASS; bundle still under 400 KB gzip.

---

### Task 7: Dokumentation und Architektur-Guard

**Files:**
- Modify: `README.md`
- Modify: `DECISIONS.md`
- Modify: `TESTING.md`
- Modify: `internal/archtest/arch_test.go`

- [x] **Step 1: Update the architecture test**

Replace the blanket `"Caddyfile"` string ban with bans for service control and Caddyfile parsing/apply: `systemctl`, `systemd`, `exec.Command("caddy`, `caddy reload`, `caddy adapt`, `caddyfile.NewAdapter`, and direct `Caddyfile` file reads. Keep the process-execution test. Add a comment that the renderer is preview-only.

- [x] **Step 2: Update docs**

README: replace "Kein Caddyfile" with "kein Caddyfile-Zugriff/Apply; read-only Caddyfile-Vorschau". Document the structured handler editor, advanced create, and the approximation warning. DECISIONS: add D24 (structured handler editor) and D25 (Caddyfile preview is a custom, read-only approximation; no JSON→Caddyfile exporter in Caddy). TESTING: add acceptance cases for nested handler editing, create-from-route, render endpoint, and generated Caddyfile adapting in Caddy.

- [x] **Step 3: Run the checks**

Run: `go test ./internal/archtest ./internal/...`
Expected: PASS.

---

### Task 8: Full verification and live rollout

- [x] **Step 1: Run the full Go and frontend suite**

Run: `make build`, `make test`, `make lint`, `make integration`
Expected: all green; both `dist/devproxy-linux-amd64` and `dist/devproxy-linux-arm64` exist.

- [x] **Step 2: Restart the live server with the new binary**

Stop the running PID with `SIGTERM`, then run `/tmp/opencode/detach-devproxy.sh`. Read the new password from `~/.local/share/devproxy/start.out` (0600).

- [x] **Step 3: Browser verification**

Log in at `https://cm.bot-tower.com`; open the existing `ai`/`mcp` routes and verify the full nested handler tree (encode, headers, multiple reverse_proxy) is visible in the form. Check the Caddyfile tab and the preview toggle. Create a preview via the advanced page and inspect the diff/Caddyfile without applying to the live Caddy. Verify no console errors and that existing routes are untouched.

- [x] **Step 4: Report**

Report the new password, what was verified, the Caddyfile approximation limit, and that no Caddyfile is ever applied.
