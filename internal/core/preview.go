package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"caddykit/internal/model"
	"caddykit/internal/state"
)

type Kind string

const (
	KindCreate       Kind = "create"
	KindEdit         Kind = "edit"
	KindDelete       Kind = "delete"
	KindAdopt        Kind = "adopt"
	KindUnmanage     Kind = "unmanage"
	KindTakeCurrent  Kind = "take-current"
	KindApplyDesired Kind = "apply-desired"
	KindUndo         Kind = "undo"
	KindRestore      Kind = "restore"
)

// Preview is both the confirmation payload and the commit request. Apply
// re-reads Caddy and refuses to overwrite if Base no longer matches.
type Preview struct {
	Kind        Kind           `json:"kind"`
	RouteID     string         `json:"route_id,omitempty"`
	Diff        []model.Change `json:"diff"`
	ETag        string         `json:"etag,omitempty"`
	Route       *model.Route   `json:"route,omitempty"`
	Base        *model.Route   `json:"base,omitempty"`
	Index       int            `json:"index"`
	Entry       *state.Entry   `json:"entry,omitempty"`
	RemoveEntry bool           `json:"remove_entry,omitempty"`
	LocalOnly   bool           `json:"local_only,omitempty"`
	Blocked     *model.Reason  `json:"blocked,omitempty"`
	Warnings    []string       `json:"warnings,omitempty"`
	Description string         `json:"description"`
}

type Result struct {
	Kind    Kind           `json:"kind"`
	RouteID string         `json:"route_id,omitempty"`
	Route   *model.Route   `json:"route,omitempty"`
	Entry   *state.Entry   `json:"entry,omitempty"`
	Diff    []model.Change `json:"diff"`
	Message string         `json:"message"`
}

func blocked(p *Preview, code, msg string) *Preview {
	p.Blocked = &model.Reason{Code: code, Message: msg}
	return p
}

func clonePtr(r *model.Route) *model.Route {
	if r == nil {
		return nil
	}
	c := (*r).Clone()
	return &c
}

func (s *Service) redact(d []model.Change) []model.Change {
	return model.Redact(d, s.RedactPatterns)
}

// PreviewCreate builds a create preview.
func (s *Service) PreviewCreate(ctx context.Context, sp model.Spec, source string, tref *state.TemplateRef) (*Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.previewCreateLocked(ctx, sp, source, tref)
}

func (s *Service) previewCreateLocked(ctx context.Context, sp model.Spec, source string, tref *state.TemplateRef) (*Preview, error) {
	if err := sp.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if sp.InsecureSkipVerify && !s.AllowInsecure {
		return nil, fmt.Errorf("%w: insecure_skip_verify requires --allow-insecure", ErrInvalid)
	}
	if source == "" {
		source = "create"
	}
	sp.ID = model.NewID()
	desired := sp.Build()
	p := &Preview{Kind: KindCreate, RouteID: sp.ID, Route: &desired, Index: 0, Description: fmt.Sprintf("create route %s -> %s", sp.Host, sp.Upstream)}
	p.Diff = s.redact(model.DiffRoute(nil, &desired, 0))

	sd, err := s.readServer(ctx)
	if err != nil {
		return nil, err
	}
	p.ETag = sd.ETag
	if dup := model.DuplicateIDs(sd.Routes); dup[sp.ID] > 0 {
		return blocked(p, BlockDupID, "the new @id already exists in Caddy"), nil
	}
	if i := collideWith(desired, sd.Routes, -1); i >= 0 {
		return blocked(p, BlockForeign, fmt.Sprintf("collides with existing route %q", describeRoute(sd.Routes[i]))), nil
	}
	now := time.Now()
	entry := state.Entry{ID: sp.ID, Snapshot: desired, Anchor: state.Anchor{Index: 0}, AdoptedAt: now, Source: source, Template: tref}
	p.Entry = &entry
	return p, nil
}

// PreviewCreateJSON builds a create preview from a complete route tree. The
// caller keeps ownership of the route shape; the service assigns a managed ID
// and applies the same safety checks as the form create path.
func (s *Service) PreviewCreateJSON(ctx context.Context, raw model.Route, source string) (*Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if source == "" {
		source = "create"
	}
	desired := raw.Clone()
	id := model.NewID()
	p := &Preview{Kind: KindCreate, RouteID: id, Index: 0}
	if old, ok := desired["@id"]; ok && old != "" {
		p.Warnings = append(p.Warnings, fmt.Sprintf("@id %v replaced with %s for management", old, id))
	}
	desired["@id"] = id
	if err := model.ValidateJSONRoute(desired, ""); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if model.HasInsecureSkipVerify(desired) && !s.AllowInsecure {
		return nil, fmt.Errorf("%w: insecure_skip_verify requires --allow-insecure", ErrInvalid)
	}
	p.Route = &desired
	p.Description = fmt.Sprintf("create route %s", strings.Join(desired.Hosts(), ","))
	p.Diff = s.redact(model.DiffRoute(nil, &desired, 0))

	sd, err := s.readServer(ctx)
	if err != nil {
		return nil, err
	}
	p.ETag = sd.ETag
	if dup := model.DuplicateIDs(sd.Routes); dup[id] > 0 {
		return blocked(p, BlockDupID, "the new @id already exists in Caddy"), nil
	}
	if i := collideWith(desired, sd.Routes, -1); i >= 0 {
		return blocked(p, BlockForeign, fmt.Sprintf("collides with existing route %q", describeRoute(sd.Routes[i]))), nil
	}
	entry := state.Entry{ID: id, Snapshot: desired, Anchor: state.Anchor{Index: 0}, AdoptedAt: time.Now(), Source: source}
	p.Entry = &entry
	return p, nil
}

// PreviewEditForm builds a form edit preview. Editing a foreign route adopts it.
func (s *Service) PreviewEditForm(ctx context.Context, sel string, sp model.Spec) (*Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, sd, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	t, err := s.target(ctx, sd.Routes, st, sel)
	if err != nil {
		return nil, err
	}
	return s.previewEditLocked(sd, st, t, &sp, nil)
}

// PreviewEditJSON builds a JSON editor preview.
func (s *Service) PreviewEditJSON(ctx context.Context, sel string, raw model.Route) (*Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, sd, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	t, err := s.target(ctx, sd.Routes, st, sel)
	if err != nil {
		return nil, err
	}
	return s.previewEditLocked(sd, st, t, nil, &raw)
}

func (s *Service) previewEditLocked(sd *serverData, st *state.State, t *Target, sp *model.Spec, raw *model.Route) (*Preview, error) {
	if t.Route == nil {
		p := &Preview{Kind: KindEdit, RouteID: t.ID, ETag: sd.ETag, Index: t.Index}
		return blocked(p, BlockRouteMissing, "route is missing in Caddy; resolve the conflict first"), nil
	}
	if t.ID == SelfID || (t.Route.ID() == SelfID) {
		p := &Preview{Kind: KindEdit, RouteID: SelfID, ETag: sd.ETag}
		return blocked(p, BlockSelf, "caddykit/self can only be changed by the web lifecycle"), nil
	}
	cur := t.Route.Clone()
	p := &Preview{Kind: KindEdit, RouteID: t.ID, Base: clonePtr(t.Route), ETag: sd.ETag, Index: t.Index}
	adopting := !t.Managed
	var entry state.Entry
	if t.Managed {
		entry = *t.Entry
	}
	if adopting {
		entry = state.Entry{AdoptedAt: time.Now(), Source: "adopt"}
		p.Warnings = append(p.Warnings, "saving adopts this previously unmanaged route; it will be restored automatically if deleted externally")
	}
	id := cur.ID()
	if sp != nil {
		_, class, reason := model.Extract(cur)
		if reason != nil || class != model.FormEditable {
			p.Blocked = reason
			if p.Blocked == nil {
				p.Blocked = &model.Reason{Code: BlockJSONOnly, Message: "route is not form-editable"}
			}
			if p.Blocked.Code == "" {
				p.Blocked.Code = BlockJSONOnly
			}
			return p, nil
		}
		if id == "" {
			id = model.NewID()
			p.Warnings = append(p.Warnings, "@id "+id+" assigned for management")
		}
		sp.ID = id
		if err := sp.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if sp.InsecureSkipVerify && !s.AllowInsecure {
			return nil, fmt.Errorf("%w: insecure_skip_verify requires --allow-insecure", ErrInvalid)
		}
		desired := sp.Build()
		p.Route = &desired
		p.Index = t.Index
		if i := collideWith(desired, sd.Routes, t.Index); i >= 0 {
			return blocked(p, BlockForeign, fmt.Sprintf("collides with existing route %q", describeRoute(sd.Routes[i]))), nil
		}
	} else {
		if id != "" {
			if raw.ID() != id {
				return nil, fmt.Errorf("%w: @id must not change", ErrInvalid)
			}
		} else {
			id = model.NewID()
			(*raw)["@id"] = id
			p.Warnings = append(p.Warnings, "@id "+id+" assigned for management")
		}
		if err := model.ValidateJSONRoute(*raw, id); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if model.HasInsecureSkipVerify(*raw) && !s.AllowInsecure {
			return nil, fmt.Errorf("%w: insecure_skip_verify requires --allow-insecure", ErrInvalid)
		}
		desired := raw.Clone()
		p.Route = &desired
		p.Index = t.Index
		if i := collideWith(desired, sd.Routes, t.Index); i >= 0 {
			return blocked(p, BlockForeign, fmt.Sprintf("collides with existing route %q", describeRoute(sd.Routes[i]))), nil
		}
	}
	entry.ID = id
	entry.Snapshot = p.Route.Clone()
	entry.Anchor = state.Anchor{Index: t.Index}
	if entry.AdoptedAt.IsZero() {
		entry.AdoptedAt = time.Now()
	}
	p.Entry = &entry
	p.RouteID = id
	p.Diff = s.redact(model.DiffRoute(t.Route, p.Route, t.Index))
	p.Description = fmt.Sprintf("edit route %s", describeRoute(*t.Route))
	return p, nil
}

func (s *Service) PreviewDelete(ctx context.Context, sel string) (*Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, sd, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	t, err := s.target(ctx, sd.Routes, st, sel)
	if err != nil {
		return nil, err
	}
	p := &Preview{Kind: KindDelete, RouteID: t.ID, ETag: sd.ETag}
	if t.ID == SelfID {
		return blocked(p, BlockSelf, "caddykit/self can only be changed by the web lifecycle"), nil
	}
	if !t.Managed {
		return blocked(p, BlockNotManaged, "only managed routes can be deleted; adopt it first"), nil
	}
	if t.Route == nil {
		return blocked(p, BlockRouteMissing, "route is already missing; resolve the conflict instead"), nil
	}
	p.Base = clonePtr(t.Route)
	p.Index = t.Index
	p.RemoveEntry = true
	p.Diff = s.redact(model.DiffRoute(t.Route, nil, t.Index))
	p.Description = fmt.Sprintf("delete route %s", describeRoute(*t.Route))
	return p, nil
}

func (s *Service) PreviewAdopt(ctx context.Context, sel string) (*Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, sd, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	t, err := s.target(ctx, sd.Routes, st, sel)
	if err != nil {
		return nil, err
	}
	return s.previewAdoptLocked(sd, st, t)
}

func (s *Service) previewAdoptLocked(sd *serverData, st *state.State, t *Target) (*Preview, error) {
	p := &Preview{Kind: KindAdopt, RouteID: t.ID, ETag: sd.ETag, Index: t.Index}
	if t.Route == nil {
		return blocked(p, BlockRouteMissing, "route does not exist"), nil
	}
	if t.ID == SelfID {
		return blocked(p, BlockSelf, "caddykit/self is managed by the web lifecycle"), nil
	}
	if t.Managed {
		return blocked(p, BlockAlreadyManaged, "route is already managed"), nil
	}
	if dup := model.DuplicateIDs(sd.Routes); t.ID != "" && dup[t.ID] > 1 {
		return blocked(p, BlockDupID, "@id occurs more than once; resolve the duplicate first"), nil
	}
	for i := 0; i < t.Index; i++ {
		if sd.Routes[i].ID() != SelfID && sd.Routes[i].Terminal() && model.RoutesCollide(sd.Routes[i], *t.Route) {
			return blocked(p, BlockForeign, fmt.Sprintf("route %q is shadowed by an earlier terminal route and cannot be restored safely", describeRoute(*t.Route))), nil
		}
	}
	cur := t.Route.Clone()
	p.Base = clonePtr(t.Route)
	desired := cur.Clone()
	if desired.ID() == "" {
		id := model.NewID()
		desired["@id"] = id
		p.Warnings = append(p.Warnings, "@id "+id+" assigned for management")
	}
	p.Route = clonePtr(&desired)
	p.RouteID = desired.ID()
	p.Entry = &state.Entry{ID: desired.ID(), Snapshot: desired, Anchor: state.Anchor{Index: t.Index}, AdoptedAt: time.Now(), Source: "adopt"}
	p.Description = fmt.Sprintf("adopt route %s", describeRoute(*t.Route))
	p.Warnings = append(p.Warnings, "adopted routes are restored automatically if they disappear externally; delete or unmanage to remove them permanently")
	p.Diff = s.redact(model.DiffRoute(&cur, &desired, t.Index))
	return p, nil
}

func (s *Service) PreviewUnmanage(ctx context.Context, sel string) (*Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, sd, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	t, err := s.target(ctx, sd.Routes, st, sel)
	if err != nil {
		return nil, err
	}
	return s.previewUnmanageLocked(sd, st, t)
}

func (s *Service) previewUnmanageLocked(sd *serverData, st *state.State, t *Target) (*Preview, error) {
	p := &Preview{Kind: KindUnmanage, RouteID: t.ID, ETag: sd.ETag, Index: t.Index}
	if t.ID == SelfID {
		return blocked(p, BlockSelf, "caddykit/self is managed by the web lifecycle"), nil
	}
	if !t.Managed {
		return blocked(p, BlockNotManaged, "route is not managed"), nil
	}
	p.Base = clonePtr(t.Route)
	p.RemoveEntry = true
	p.LocalOnly = true
	p.Diff = []model.Change{{Op: "remove", Path: "/state/routes/" + t.ID, Before: t.Entry.Snapshot}}
	p.Description = "unmanage route; Caddy configuration stays unchanged"
	return p, nil
}

func (s *Service) loadLocked(ctx context.Context) (*state.State, *serverData, error) {
	st, err := s.Store.LoadState()
	if err != nil {
		return nil, nil, err
	}
	sd, err := s.readServer(ctx)
	if err != nil {
		return nil, nil, err
	}
	return st, sd, nil
}

// checkCollision rejects a route that would shadow a foreign route. A restore
// at its old anchor may sit before later routes (that was its original
// position); only earlier terminal routes block it.
func checkCollision(o op, routes []model.Route, idx int) *BlockedError {
	if o.desired == nil {
		return nil
	}
	if o.base == nil && (o.kind == KindRestore || o.kind == KindApplyDesired) {
		for i := 0; i < idx && i < len(routes); i++ {
			if routes[i].ID() == SelfID || !routes[i].Terminal() {
				continue
			}
			if model.RoutesCollide(routes[i], *o.desired) {
				return &BlockedError{Reason: model.Reason{Code: BlockForeign, Message: fmt.Sprintf("collides with earlier terminal route %q", describeRoute(routes[i]))}}
			}
		}
		return nil
	}
	if i := collideWith(*o.desired, routes, idx); i >= 0 {
		return &BlockedError{Reason: model.Reason{Code: BlockForeign, Message: fmt.Sprintf("collides with route %q", describeRoute(routes[i]))}}
	}
	return nil
}

func collideWith(r model.Route, routes []model.Route, exclude int) int {
	for i := range routes {
		if i == exclude {
			continue
		}
		if routes[i].ID() == SelfID && r.ID() == SelfID {
			continue
		}
		if model.RoutesCollide(routes[i], r) {
			return i
		}
	}
	return -1
}

func describeRoute(r model.Route) string {
	hosts := strings.Join(r.Hosts(), ",")
	if id := r.ID(); id != "" {
		return hosts + " [" + id + "]"
	}
	return hosts
}
