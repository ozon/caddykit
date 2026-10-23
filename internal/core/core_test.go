package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"devproxy/internal/caddy"
	"devproxy/internal/model"
	"devproxy/internal/state"
)

// fakeCaddy emulates the parts of the admin API used by devproxy.
type fakeCaddy struct {
	mu            sync.Mutex
	routes        []model.Route
	etagN         int
	on412         func(f *fakeCaddy)
	rejectOnce    bool
	missingServer bool
}

func (f *fakeCaddy) etag() string {
	return fmt.Sprintf(`"/config/apps/http/servers/srv0 %d"`, f.etagN)
}

func (f *fakeCaddy) get() []model.Route {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.Route, len(f.routes))
	copy(out, f.routes)
	return out
}

func (f *fakeCaddy) set(routes []model.Route) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes = routes
	f.etagN++
}

func (f *fakeCaddy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/config/":
		w.Header().Set("Etag", `"/config/ 1"`)
		_, _ = w.Write([]byte(`{}`))
	case r.URL.Path == "/config/apps/http/servers":
		_, _ = w.Write([]byte(`{"srv0":{"listen":[":80"]}}`))
	case r.URL.Path == "/config/apps/http/servers/srv0":
		if f.missingServer {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Etag", f.etag())
		body, _ := json.Marshal(map[string]any{"listen": []any{":80"}, "routes": f.routes})
		_, _ = w.Write(body)
	case r.URL.Path == "/config/apps/http/servers/srv0/routes" && (r.Method == http.MethodPut || r.Method == http.MethodPatch):
		if f.rejectOnce {
			f.rejectOnce = false
			if f.on412 != nil {
				cb := f.on412
				f.on412 = nil
				cb(f)
			}
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		if r.Header.Get("If-Match") != f.etag() {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		var raw []any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		if err := dec.Decode(&raw); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		routes := make([]model.Route, 0, len(raw))
		for _, e := range raw {
			routes = append(routes, model.Route(e.(map[string]any)))
		}
		f.routes = routes
		f.etagN++
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newTestService(t *testing.T) (*Service, *fakeCaddy) {
	t.Helper()
	f := &fakeCaddy{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := caddy.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveState(state.NewState("srv0")); err != nil {
		t.Fatal(err)
	}
	return New(c, st, "srv0"), f
}

func mustPreview(p *Preview, err error) *Preview {
	if err != nil {
		panic(err)
	}
	if p.Blocked != nil {
		panic("blocked: " + p.Blocked.Code + ": " + p.Blocked.Message)
	}
	return p
}

func TestCreateEditDeleteUndo(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	sp := model.Spec{Host: "app.example.com", Upstream: "127.0.0.1:3000", Path: "/api/*", StripPrefix: "/api"}
	p := mustPreview(svc.PreviewCreate(ctx, sp, "create", nil))
	res, err := svc.Apply(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	id := res.RouteID
	routes := f.get()
	if len(routes) != 1 || routes[0].ID() != id {
		t.Fatalf("route not created: %+v", routes)
	}
	st, _ := svc.Store.LoadState()
	if _, ok := st.Routes[id]; !ok {
		t.Fatal("state entry missing")
	}
	if u, _ := svc.Store.LoadUndo(); u == nil || u.Kind != "create" {
		t.Fatal("undo missing")
	}

	// Undo create.
	if _, err := svc.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.get()) != 0 {
		t.Fatal("undo did not delete the route")
	}
	if st, _ := svc.Store.LoadState(); len(st.Routes) != 0 {
		t.Fatal("undo did not clear state")
	}
	if u, _ := svc.Store.LoadUndo(); u != nil {
		t.Fatal("undo step not cleared")
	}

	// Create again and edit.
	p = mustPreview(svc.PreviewCreate(ctx, sp, "create", nil))
	if _, err := svc.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	sp.Upstream = "127.0.0.1:4000"
	ep := mustPreview(svc.PreviewEditForm(ctx, "app.example.com", sp))
	if _, err := svc.Apply(ctx, ep); err != nil {
		t.Fatal(err)
	}
	if got := f.get()[0].UpstreamDial(); got != "127.0.0.1:4000" {
		t.Fatalf("edit not applied: %s", got)
	}
	if _, err := svc.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.get()[0].UpstreamDial(); got != "127.0.0.1:3000" {
		t.Fatalf("undo edit failed: %s", got)
	}
}

func TestExternalDeleteRestoreAndTombstone(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	p := mustPreview(svc.PreviewCreate(ctx, model.Spec{Host: "app.example.com", Upstream: "127.0.0.1:3000"}, "create", nil))
	res, _ := svc.Apply(ctx, p)
	f.set(nil)
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.get()) != 1 {
		t.Fatal("missing managed route was not restored")
	}
	// Deleting through the tool must prevent restoration.
	dp := mustPreview(svc.PreviewDelete(ctx, res.RouteID))
	if _, err := svc.Apply(ctx, dp); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.get()) != 0 {
		t.Fatal("deleted route was restored")
	}
	// Undo delete brings it back.
	if _, err := svc.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.get()) != 1 {
		t.Fatal("undo delete failed")
	}
}

func TestExternalModifyConflictAndThreeResolutions(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	p := mustPreview(svc.PreviewCreate(ctx, model.Spec{Host: "app.example.com", Upstream: "127.0.0.1:3000"}, "create", nil))
	res, _ := svc.Apply(ctx, p)
	id := res.RouteID
	mod := f.get()[0].Clone()
	mod["handle"].([]any)[0].(map[string]any)["upstreams"] = []any{map[string]any{"dial": "127.0.0.1:9999"}}
	f.set([]model.Route{mod})

	cs, err := svc.Conflicts(ctx)
	if err != nil || len(cs) != 1 || cs[0].Kind != "EXTERNALLY_MODIFIED" {
		t.Fatalf("conflicts: %+v %v", cs, err)
	}
	// take-current
	tp := mustPreview(svc.PreviewResolve(ctx, id, "take-current"))
	if _, err := svc.Apply(ctx, tp); err != nil {
		t.Fatal(err)
	}
	st, _ := svc.Store.LoadState()
	if got := st.Routes[id].Snapshot.UpstreamDial(); got != "127.0.0.1:9999" {
		t.Fatalf("take-current not stored: %s", got)
	}
	// undo take-current
	if _, err := svc.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	// modify externally again, then apply-desired
	mod2 := f.get()[0].Clone()
	mod2["handle"].([]any)[0].(map[string]any)["upstreams"] = []any{map[string]any{"dial": "127.0.0.1:9999"}}
	f.set([]model.Route{mod2})
	ap := mustPreview(svc.PreviewResolve(ctx, id, "apply-desired"))
	if _, err := svc.Apply(ctx, ap); err != nil {
		t.Fatal(err)
	}
	if got := f.get()[0].UpstreamDial(); got != "127.0.0.1:3000" {
		t.Fatalf("apply-desired failed: %s", got)
	}
	// undo apply-desired
	if _, err := svc.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	// unmanage leaves Caddy unchanged
	if _, err := svc.Undo(ctx); err == nil {
		t.Fatal("undo should not be available after unmanage")
	}
	um := mustPreview(svc.PreviewResolve(ctx, id, "unmanage"))
	if _, err := svc.Apply(ctx, um); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.Store.LoadState(); len(st.Routes) != 0 {
		t.Fatal("unmanage did not clear the binding")
	}
	if len(f.get()) != 1 {
		t.Fatal("unmanage changed Caddy")
	}
}

func TestUndoBlockedByExternalChange(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	p := mustPreview(svc.PreviewCreate(ctx, model.Spec{Host: "a.example.com", Upstream: "h:1"}, "create", nil))
	_, _ = svc.Apply(ctx, p)
	mod := f.get()[0].Clone()
	mod["terminal"] = false
	f.set([]model.Route{mod})
	if _, err := svc.Undo(ctx); !errors.Is(err, ErrUndoBlocked) {
		t.Fatalf("expected ErrUndoBlocked, got %v", err)
	}
}

func TestPreviewStaleOnModifiedTarget(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	p := mustPreview(svc.PreviewCreate(ctx, model.Spec{Host: "a.example.com", Upstream: "h:1"}, "create", nil))
	res, _ := svc.Apply(ctx, p)
	ep := mustPreview(svc.PreviewEditForm(ctx, res.RouteID, model.Spec{Host: "a.example.com", Upstream: "h:2"}))
	mod := f.get()[0].Clone()
	mod["terminal"] = false
	f.set([]model.Route{mod})
	if _, err := svc.Apply(ctx, ep); !errors.Is(err, ErrStale) {
		t.Fatalf("expected ErrStale, got %v", err)
	}
}

func TestPreconditionRetry(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	p := mustPreview(svc.PreviewCreate(ctx, model.Spec{Host: "a.example.com", Upstream: "h:1"}, "create", nil))
	f.rejectOnce = true
	f.on412 = func(f *fakeCaddy) {
		f.routes = append(f.routes, model.Spec{Host: "foreign.example.com", Upstream: "h:9"}.Build())
		f.etagN++
	}
	if _, err := svc.Apply(ctx, p); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if len(f.get()) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(f.get()))
	}
	if f.get()[0].ID() != p.RouteID {
		t.Fatal("managed route not inserted at index 0")
	}
}

func TestCollisionBlocksCreate(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	f.set([]model.Route{model.Spec{Host: "*.example.com", Upstream: "h:1"}.Build()})
	p, err := svc.PreviewCreate(ctx, model.Spec{Host: "app.example.com", Upstream: "h:2"}, "create", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Blocked == nil || p.Blocked.Code != BlockForeign {
		t.Fatalf("expected foreign collision, got %+v", p.Blocked)
	}
}

func TestSelfProtected(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	self := model.Route{"@id": SelfID, "match": []any{map[string]any{"host": []any{"dev.example.com"}}}, "handle": []any{map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": "127.0.0.1:1"}}}}, "terminal": true}
	f.set([]model.Route{self})
	for _, fn := range []func() (*Preview, error){
		func() (*Preview, error) { return svc.PreviewDelete(ctx, SelfID) },
		func() (*Preview, error) { return svc.PreviewUnmanage(ctx, SelfID) },
		func() (*Preview, error) {
			return svc.PreviewEditForm(ctx, SelfID, model.Spec{Host: "x.example.com", Upstream: "h:1"})
		},
	} {
		p, err := fn()
		if err != nil {
			t.Fatal(err)
		}
		if p.Blocked == nil || p.Blocked.Code != BlockSelf {
			t.Fatalf("self not protected: %+v", p.Blocked)
		}
	}
}

func TestJournalRecoveryMatrix(t *testing.T) {
	ctx := context.Background()
	base := model.Spec{Host: "a.example.com", Upstream: "h:1"}.Build()
	newRoute := model.Spec{Host: "a.example.com", Upstream: "h:2"}.Build()
	newRoute["@id"] = "devproxy/r/01920000-0000-7000-8000-000000000001"
	entry := &state.Entry{ID: newRoute.ID(), Snapshot: newRoute, Source: "create", AdoptedAt: time.Now()}

	cases := []struct {
		name      string
		phase     string
		caddy     []model.Route
		local     bool
		wantKeep  bool
		wantState bool
	}{
		{"prepared old", state.PhasePrepared, []model.Route{base}, false, false, false},
		{"prepared new", state.PhasePrepared, []model.Route{newRoute}, false, false, true},
		{"caddy_written new", state.PhaseCaddyWritten, []model.Route{newRoute}, false, false, true},
		{"no match", state.PhasePrepared, []model.Route{model.Spec{Host: "z.example.com", Upstream: "h:3"}.Build()}, false, true, false},
		{"local only", state.PhaseCaddyWritten, []model.Route{base}, true, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, f := newTestService(t)
			f.set(c.caddy)
			j := &state.Journal{
				OpID: "op", Kind: "edit", RouteID: newRoute.ID(), Phase: c.phase, LocalOnly: c.local,
				Old: &state.Image{Route: base, Index: 0},
				New: &state.Image{Route: newRoute, Entry: entry, Index: 0},
			}
			if err := svc.Store.SaveJournal(j); err != nil {
				t.Fatal(err)
			}
			if err := svc.Recover(ctx); err != nil {
				t.Fatalf("recover: %v", err)
			}
			got, _ := svc.Store.LoadJournal()
			if c.wantKeep {
				if got == nil || got.Phase != state.PhaseUnclear {
					t.Fatalf("expected unclear journal, got %+v", got)
				}
			} else if got != nil {
				t.Fatalf("journal not cleared: %+v", got)
			}
			st, _ := svc.Store.LoadState()
			_, has := st.Routes[entry.ID]
			if has != c.wantState {
				t.Fatalf("state entry present=%v want %v", has, c.wantState)
			}
		})
	}
}

func TestAdoptForeignIDAndRestore(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	foreign := model.Spec{Host: "legacy.example.com", Upstream: "h:1"}.Build()
	f.set([]model.Route{foreign})
	p := mustPreview(svc.PreviewAdopt(ctx, "legacy.example.com"))
	if p.RouteID == "" || p.Route == nil {
		t.Fatalf("adopt preview: %+v", p)
	}
	if _, err := svc.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	if f.get()[0].ID() == "" {
		t.Fatal("adopt did not assign an @id")
	}
	f.set(nil)
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.get()) != 1 {
		t.Fatal("adopted route not restored")
	}
	// Unmanage stops restoration.
	um := mustPreview(svc.PreviewUnmanage(ctx, p.RouteID))
	if _, err := svc.Apply(ctx, um); err != nil {
		t.Fatal(err)
	}
	f.set(nil)
	_ = svc.Reconcile(ctx)
	if len(f.get()) != 0 {
		t.Fatal("unmanaged route was restored")
	}
}

func TestDuplicateIDConflict(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	p := mustPreview(svc.PreviewCreate(ctx, model.Spec{Host: "a.example.com", Upstream: "h:1"}, "create", nil))
	if _, err := svc.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	dup := f.get()[0].Clone()
	f.set([]model.Route{f.get()[0], dup})
	cs, err := svc.Conflicts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cs {
		if c.Kind == "DUP_ID" {
			found = true
			if c.Blocked["apply-desired"].Code != BlockDupID {
				t.Fatalf("expected DUP_ID block, got %+v", c.Blocked)
			}
		}
	}
	if !found {
		t.Fatalf("no DUP_ID conflict: %+v", cs)
	}
}

func TestForeignCollisionBlocksRestoreAtAnchor(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	other := model.Spec{Host: "other.example.com", Upstream: "h:1"}.Build()
	target := model.Spec{Host: "app.example.com", Upstream: "h:2"}.Build()
	f.set([]model.Route{other, target})
	p := mustPreview(svc.PreviewAdopt(ctx, "app.example.com"))
	id := p.RouteID
	if _, err := svc.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	// External delete, then put a terminal wildcard route before the anchor.
	f.set(nil)
	blocker := model.Spec{Host: "*.example.com", Upstream: "h:9"}.Build()
	f.set([]model.Route{blocker})
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.get()) != 1 || f.get()[0].ID() != "" {
		t.Fatalf("route was restored despite a foreign collision: %+v", f.get())
	}
	cs, err := svc.Conflicts(ctx)
	if err != nil || len(cs) != 1 || cs[0].Kind != "FOREIGN_COLLISION" {
		t.Fatalf("conflicts: %+v %v", cs, err)
	}
	ap, err := svc.PreviewResolve(ctx, id, "apply-desired")
	if err != nil {
		t.Fatal(err)
	}
	if ap.Blocked == nil || ap.Blocked.Code != BlockForeign {
		t.Fatalf("apply-desired should be blocked: %+v", ap.Blocked)
	}
	um := mustPreview(svc.PreviewResolve(ctx, id, "unmanage"))
	if _, err := svc.Apply(ctx, um); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptBlockedWhenShadowed(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	f.set([]model.Route{
		model.Spec{Host: "*.example.com", Upstream: "h:9"}.Build(),
		model.Spec{Host: "app.example.com", Upstream: "h:1"}.Build(),
	})
	p, err := svc.PreviewAdopt(ctx, "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if p.Blocked == nil || p.Blocked.Code != BlockForeign {
		t.Fatalf("adopt should be blocked when shadowed: %+v", p.Blocked)
	}
}

func TestListensPublic(t *testing.T) {
	cases := []struct {
		listen any
		want   bool
	}{
		{nil, true},
		{[]any{}, true},
		{[]any{":80"}, true},
		{[]any{":443"}, true},
		{[]any{"0.0.0.0:443"}, true},
		{[]any{":8080"}, false},
		{[]any{"127.0.0.1:80", ":9000"}, true},
	}
	for i, c := range cases {
		server := map[string]any{}
		if c.listen != nil {
			server["listen"] = c.listen
		}
		if got := listensPublic(server); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}

func TestUndoAdoptAssignedID(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	f.set([]model.Route{model.Spec{Host: "legacy.example.com", Upstream: "h:1"}.Build()})
	p := mustPreview(svc.PreviewAdopt(ctx, "legacy.example.com"))
	id := p.RouteID
	if _, err := svc.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	if f.get()[0].ID() != id {
		t.Fatal("adopt did not assign the id")
	}
	if _, err := svc.Undo(ctx); err != nil {
		t.Fatalf("undo adopt: %v", err)
	}
	routes := f.get()
	if len(routes) != 1 {
		t.Fatalf("route lost: %+v", routes)
	}
	if routes[0].ID() != "" {
		t.Fatalf("undo adopt left @id %q", routes[0].ID())
	}
	if st, _ := svc.Store.LoadState(); len(st.Routes) != 0 {
		t.Fatal("undo adopt left a state entry")
	}
}

func TestJSONEditCannotBypassAllowInsecure(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	p := mustPreview(svc.PreviewCreate(ctx, model.Spec{Host: "a.example.com", Upstream: "h:1"}, "create", nil))
	id := p.RouteID
	if _, err := svc.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	raw := f.get()[0].Clone()
	raw["handle"].([]any)[0].(map[string]any)["transport"] = map[string]any{
		"protocol": "http", "tls": map[string]any{"insecure_skip_verify": true},
	}
	if _, err := svc.PreviewEditJSON(ctx, id, raw); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected allow-insecure gate, got %v", err)
	}
	svc.AllowInsecure = true
	if _, err := svc.PreviewEditJSON(ctx, id, raw); err != nil {
		t.Fatalf("edit should be allowed with AllowInsecure: %v", err)
	}
}

func TestMissingServerConflict(t *testing.T) {
	ctx := context.Background()
	svc, f := newTestService(t)
	p := mustPreview(svc.PreviewCreate(ctx, model.Spec{Host: "a.example.com", Upstream: "h:1"}, "create", nil))
	if _, err := svc.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	f.missingServer = true
	cs, err := svc.Conflicts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Kind != "MISSING_CONTEXT" || cs[0].Blocked["apply-desired"].Code != BlockMissingContext {
		t.Fatalf("missing context conflict: %+v", cs)
	}
}

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
	if err != nil {
		t.Fatal(err)
	}
	p := mustPreview(svc.PreviewCreateJSON(ctx, raw, "create"))
	if p.Route == nil || p.Entry == nil || p.Route.ID() == "" {
		t.Fatalf("incomplete preview: %+v", p)
	}
	res, err := svc.Apply(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	routes := f.get()
	if len(routes) != 1 || routes[0].ID() != res.RouteID {
		t.Fatalf("route not created: %+v", routes)
	}
	h := routes[0]["handle"].([]any)[0].(map[string]any)
	if h["handler"] != "subroute" {
		t.Fatalf("nested handler lost: %+v", h)
	}
}

func TestPreviewCreateJSONRejectsBadNestedHandler(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	raw, err := model.Decode([]byte(`{
		"handle":[{"handler":"subroute","routes":[{"handle":[{}]}]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PreviewCreateJSON(ctx, raw, "create"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}
