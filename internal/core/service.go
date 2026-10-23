// Package core implements the shared business logic for CLI and web UI.
// All mutations are serialized in-process and follow spec §4.1.
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"caddykit/internal/caddy"
	"caddykit/internal/logging"
	"caddykit/internal/model"
	"caddykit/internal/state"
)

const SelfID = "caddykit/self"

var (
	ErrNotFound       = errors.New("route not found")
	ErrAmbiguous      = errors.New("selector is ambiguous")
	ErrInvalid        = errors.New("invalid input")
	ErrBlocked        = errors.New("operation blocked")
	ErrStale          = errors.New("preview is stale; review the refreshed diff")
	ErrNoUndo         = errors.New("no undo step available")
	ErrUndoBlocked    = errors.New("undo is not safe")
	ErrSelfProtected  = errors.New("the caddykit/self route is protected")
	ErrJournalOpen    = errors.New("an operation journal is open")
	ErrMissingContext = errors.New("missing Caddy context")
	ErrServerMismatch = errors.New("state belongs to a different Caddy server")
)

type BlockedError struct{ Reason model.Reason }

func (e *BlockedError) Error() string {
	return "blocked (" + e.Reason.Code + "): " + e.Reason.Message
}
func (e *BlockedError) Unwrap() error { return ErrBlocked }

type AmbiguousError struct {
	Selector   string
	Candidates []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("selector %q is ambiguous: %s", e.Selector, strings.Join(e.Candidates, ", "))
}
func (e *AmbiguousError) Unwrap() error { return ErrAmbiguous }

// Block reason codes, machine readable (spec §5.2).
const (
	BlockSelf           = "SELF_PROTECTED"
	BlockJSONOnly       = "JSON_ONLY"
	BlockRouteMissing   = "ROUTE_MISSING"
	BlockNotManaged     = "NOT_MANAGED"
	BlockAlreadyManaged = "ALREADY_MANAGED"
	BlockForeign        = "FOREIGN_COLLISION"
	BlockMissingContext = "MISSING_CONTEXT"
	BlockDupID          = "DUP_ID"
	BlockOpUnclear      = "OP_UNCLEAR"
	BlockStale          = "PREVIEW_STALE"
	BlockNoUndo         = "NO_UNDO"
	BlockUndoMismatch   = "UNDO_MISMATCH"
)

type Service struct {
	Caddy          *caddy.Client
	Store          *state.Store
	Server         string
	AllowInsecure  bool
	RedactPatterns []string
	// Log, when non-nil, receives operational events such as reconcile
	// failures. The CLI leaves it nil unless --log-level asks for logs; the
	// web backend wires it to its logger.
	Log func(level logging.Level, format string, args ...any)

	mu            sync.Mutex
	lastReconcile time.Time
	lastCaddyErr  string
	conflicts     map[string]Conflict
}

func New(c *caddy.Client, st *state.Store, server string) *Service {
	return &Service{Caddy: c, Store: st, Server: server, conflicts: map[string]Conflict{}}
}

// logf reports an operational event when a logger is wired up.
func (s *Service) logf(level logging.Level, format string, args ...any) {
	if s.Log != nil {
		s.Log(level, format, args...)
	}
}

type serverData struct {
	Routes    []model.Route
	ETag      string
	HasRoutes bool
}

func (s *Service) readServer(ctx context.Context) (*serverData, error) {
	if s.Server == "" {
		return nil, fmt.Errorf("%w: no Caddy server selected", ErrMissingContext)
	}
	body, etag, err := s.Caddy.GetWithETag(ctx, caddy.ServerPath(s.Server))
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("decode server %q: %w", s.Server, err)
	}
	routes := []model.Route{}
	_, hasRoutes := obj["routes"]
	if raw, ok := obj["routes"].([]any); ok {
		for _, e := range raw {
			m, ok := e.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("server %q contains a non-object route", s.Server)
			}
			routes = append(routes, model.Route(m))
		}
	}
	return &serverData{Routes: routes, ETag: etag, HasRoutes: hasRoutes}, nil
}

func (s *Service) writeRoutes(ctx context.Context, routes []model.Route, ifMatch string, exists bool) error {
	if routes == nil {
		routes = []model.Route{}
	}
	body, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	if exists {
		return s.Caddy.Patch(ctx, caddy.RoutesPath(s.Server), body, ifMatch)
	}
	return s.Caddy.Put(ctx, caddy.RoutesPath(s.Server), body, ifMatch)
}

// ValidateServer verifies or autodetects the managed HTTP server and checks it
// against the persisted state.
func (s *Service) ValidateServer(ctx context.Context) error {
	body, _, err := s.Caddy.Get(ctx, "/config/apps/http/servers")
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var servers map[string]map[string]any
	if err := dec.Decode(&servers); err != nil {
		return fmt.Errorf("decode server list: %w", err)
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	if s.Server != "" {
		if _, ok := servers[s.Server]; !ok {
			return fmt.Errorf("caddy server %q not found; available: %s", s.Server, strings.Join(names, ", "))
		}
	} else {
		var candidates []string
		for _, name := range names {
			if listensPublic(servers[name]) {
				candidates = append(candidates, name)
			}
		}
		switch len(candidates) {
		case 1:
			s.Server = candidates[0]
		case 0:
			return fmt.Errorf("no Caddy server listening on :80/:443 found; use --server (available: %s)", strings.Join(names, ", "))
		default:
			return fmt.Errorf("multiple Caddy servers listening on :80/:443 (%s); use --server", strings.Join(candidates, ", "))
		}
	}
	st, err := s.Store.LoadState()
	if err != nil {
		return err
	}
	if st.Server != "" && len(st.Routes) > 0 && st.Server != s.Server {
		return fmt.Errorf("%w: %q, selected %q", ErrServerMismatch, st.Server, s.Server)
	}
	if st.Server == "" {
		st.Server = s.Server
		if err := s.Store.SaveState(st); err != nil {
			return err
		}
	}
	return nil
}

func listensPublic(server map[string]any) bool {
	listen, ok := server["listen"].([]any)
	if !ok || len(listen) == 0 {
		return true // Caddy defaults to :443
	}
	for _, e := range listen {
		s, _ := e.(string)
		_, port, err := net.SplitHostPort(s)
		if err != nil {
			if strings.HasSuffix(s, ":443") || strings.HasSuffix(s, ":80") {
				return true
			}
			continue
		}
		if port == "443" || port == "80" {
			return true
		}
	}
	return false
}

type RouteView struct {
	ID       string        `json:"id"`
	Managed  bool          `json:"managed"`
	Self     bool          `json:"self"`
	Host     string        `json:"host"`
	Paths    []string      `json:"paths"`
	Upstream string        `json:"upstream"`
	Class    model.Class   `json:"class"`
	Reason   *model.Reason `json:"reason,omitempty"`
	Source   string        `json:"source,omitempty"`
	Status   string        `json:"status"`
	Index    int           `json:"index"`
	Route    model.Route   `json:"route,omitempty"`
}

type Target struct {
	Route   *model.Route
	Index   int
	ID      string
	Managed bool
	Entry   *state.Entry
}

func (s *Service) target(ctx context.Context, routes []model.Route, st *state.State, sel string) (*Target, error) {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return nil, fmt.Errorf("%w: empty selector", ErrInvalid)
	}
	// UUID prefix of a managed route (>= 8 chars, unique).
	lower := strings.ToLower(sel)
	if len(lower) >= 8 && isHexPrefix(lower) {
		var matches []string
		for id := range st.Routes {
			if strings.HasPrefix(strings.ToLower(strings.TrimPrefix(id, "caddykit/r/")), lower) ||
				strings.HasPrefix(strings.ToLower(id), lower) {
				matches = append(matches, id)
			}
		}
		if len(matches) == 1 {
			return s.targetByID(routes, st, matches[0]), nil
		}
		if len(matches) > 1 {
			return nil, &AmbiguousError{Selector: sel, Candidates: model.SortStrings(matches)}
		}
	}
	// Exact @id (managed or foreign).
	for i := range routes {
		if routes[i].ID() == sel {
			t := &Target{Route: &routes[i], Index: i, ID: routes[i].ID()}
			if e, ok := st.Routes[t.ID]; ok {
				ec := e
				t.Managed, t.Entry = true, &ec
			}
			return t, nil
		}
	}
	if _, ok := st.Routes[sel]; ok {
		return s.targetByID(routes, st, sel), nil
	}
	// Unique host.
	var matches []int
	for i := range routes {
		for _, h := range routes[i].Hosts() {
			if model.HostEqual(h, sel) {
				matches = append(matches, i)
				break
			}
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%w: %q", ErrNotFound, sel)
	case 1:
		i := matches[0]
		t := &Target{Route: &routes[i], Index: i, ID: routes[i].ID()}
		if e, ok := st.Routes[t.ID]; ok {
			ec := e
			t.Managed, t.Entry = true, &ec
		}
		return t, nil
	default:
		var candidates []string
		for _, i := range matches {
			if id := routes[i].ID(); id != "" {
				candidates = append(candidates, id)
			} else {
				candidates = append(candidates, strings.Join(routes[i].Hosts(), ","))
			}
		}
		return nil, &AmbiguousError{Selector: sel, Candidates: model.SortStrings(candidates)}
	}
}

func (s *Service) targetByID(routes []model.Route, st *state.State, id string) *Target {
	t := &Target{ID: id}
	if e, ok := st.Routes[id]; ok {
		ec := e
		t.Managed, t.Entry = true, &ec
	}
	for i := range routes {
		if routes[i].ID() == id {
			t.Route, t.Index = &routes[i], i
			return t
		}
	}
	return t
}

func isHexPrefix(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && r != '-' {
			return false
		}
	}
	return true
}

func (s *Service) Resolve(ctx context.Context, sel string) (*Target, error) {
	st, err := s.Store.LoadState()
	if err != nil {
		return nil, err
	}
	sd, err := s.readServer(ctx)
	if err != nil {
		return nil, err
	}
	return s.target(ctx, sd.Routes, st, sel)
}

// List returns managed and foreign routes. all=false hides foreign routes.
func (s *Service) List(ctx context.Context, all bool) ([]RouteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked(ctx, all)
}

func (s *Service) listLocked(ctx context.Context, all bool) ([]RouteView, error) {
	st, err := s.Store.LoadState()
	if err != nil {
		return nil, err
	}
	sd, err := s.readServer(ctx)
	if err != nil {
		return nil, err
	}
	dup := model.DuplicateIDs(sd.Routes)
	seen := map[string]bool{}
	out := []RouteView{}
	for i, r := range sd.Routes {
		id := r.ID()
		v := RouteView{ID: id, Managed: false, Self: id == SelfID, Host: firstOr(r.Hosts(), ""), Paths: r.Paths(), Upstream: r.UpstreamDial(), Index: i, Route: r}
		_, class, reason := model.Extract(r)
		v.Class, v.Reason = class, reason
		if v.Self {
			v.Status = "self"
			v.Managed = true
		} else if e, ok := st.Routes[id]; ok && id != "" {
			seen[id] = true
			v.Managed = true
			v.Source = e.Source
			switch {
			case dup[id] > 1:
				v.Status = "dup_id"
			case model.Equal(e.Snapshot, r):
				v.Status = "managed_sync"
			default:
				v.Status = "externally_modified"
			}
		} else {
			v.Status = "foreign"
		}
		if !v.Managed && !all {
			continue
		}
		out = append(out, v)
	}
	// Managed routes that are missing in Caddy.
	var missing []RouteView
	for id, e := range st.Routes {
		if seen[id] {
			continue
		}
		v := RouteView{ID: id, Managed: true, Source: e.Source, Host: firstOr(e.Snapshot.Hosts(), ""), Paths: e.Snapshot.Paths(), Upstream: e.Snapshot.UpstreamDial(), Class: model.FormEditable}
		if c, blocked := s.conflictForMissing(sd.Routes, id, e); blocked {
			v.Status = strings.ToLower(c.Kind)
		} else {
			v.Status = "missing_restoreable"
		}
		missing = append(missing, v)
	}
	sort.Slice(missing, func(i, j int) bool {
		return st.Routes[missing[i].ID].Anchor.Index < st.Routes[missing[j].ID].Anchor.Index
	})
	out = append(out, missing...)
	return out, nil
}

func (s *Service) conflictForMissing(routes []model.Route, id string, e state.Entry) (Conflict, bool) {
	if j, _ := s.Store.LoadJournal(); j != nil && j.RouteID == id && j.Phase == state.PhaseUnclear {
		return Conflict{RouteID: id, Kind: "OP_UNCLEAR", Host: firstOr(e.Snapshot.Hosts(), "")}, true
	}
	if dup := model.DuplicateIDs(routes); len(dup) > 0 {
		if dup[id] > 0 {
			return Conflict{RouteID: id, Kind: "DUP_ID", Host: firstOr(e.Snapshot.Hosts(), "")}, true
		}
	}
	idx := e.Anchor.Index
	if idx < 0 {
		idx = 0
	}
	if idx > len(routes) {
		idx = len(routes)
	}
	for i := 0; i < idx; i++ {
		if routes[i].ID() == SelfID {
			continue
		}
		if routes[i].Terminal() && model.RoutesCollide(routes[i], e.Snapshot) {
			return Conflict{RouteID: id, Kind: "FOREIGN_COLLISION", Host: firstOr(e.Snapshot.Hosts(), "")}, true
		}
	}
	return Conflict{}, false
}

func (s *Service) Show(ctx context.Context, sel string) (*RouteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	views, err := s.listLocked(ctx, true)
	if err != nil {
		return nil, err
	}
	st, err := s.Store.LoadState()
	if err != nil {
		return nil, err
	}
	sd, err := s.readServer(ctx)
	if err != nil {
		return nil, err
	}
	t, err := s.target(ctx, sd.Routes, st, sel)
	if err != nil {
		return nil, err
	}
	for i := range views {
		if views[i].ID == t.ID || (t.ID == "" && views[i].Index == t.Index) {
			return &views[i], nil
		}
	}
	if t.Entry != nil {
		e := *t.Entry
		return &RouteView{ID: e.ID, Managed: true, Source: e.Source, Host: firstOr(e.Snapshot.Hosts(), ""), Paths: e.Snapshot.Paths(), Upstream: e.Snapshot.UpstreamDial(), Status: "missing_restoreable"}, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrNotFound, sel)
}

type Status struct {
	CaddyOK       bool      `json:"caddy_ok"`
	CaddyError    string    `json:"caddy_error,omitempty"`
	Server        string    `json:"server"`
	LastReconcile time.Time `json:"last_reconcile"`
	StateRoutes   int       `json:"state_routes"`
	Conflicts     int       `json:"conflicts"`
	UndoAvailable bool      `json:"undo_available"`
}

func (s *Service) Status(ctx context.Context) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Server: s.Server, LastReconcile: s.lastReconcile}
	if err := s.Caddy.Check(ctx); err != nil {
		st.CaddyOK = false
		st.CaddyError = err.Error()
	} else {
		st.CaddyOK = true
	}
	if stt, err := s.Store.LoadState(); err == nil {
		st.StateRoutes = len(stt.Routes)
	}
	if u, err := s.Store.LoadUndo(); err == nil && u != nil {
		st.UndoAvailable = true
	}
	if cs, err := s.computeConflictsLocked(ctx); err == nil {
		st.Conflicts = len(cs)
	}
	return st
}

func firstOr(in []string, def string) string {
	if len(in) == 0 {
		return def
	}
	return in[0]
}

// IsCaddyUnreachable classifies transport-level errors for exit code 4.
func IsCaddyUnreachable(err error) bool {
	var ue *url.Error
	if errors.As(err, &ue) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne)
}
