package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"caddykit/internal/caddy"
	"caddykit/internal/logging"
	"caddykit/internal/model"
	"caddykit/internal/state"
)

type Conflict struct {
	RouteID string                  `json:"route_id"`
	Kind    string                  `json:"kind"`
	Host    string                  `json:"host"`
	Reason  string                  `json:"reason"`
	Blocked map[string]model.Reason `json:"blocked,omitempty"`
	Current *model.Route            `json:"current,omitempty"`
	Desired *model.Route            `json:"desired,omitempty"`
}

func (s *Service) Conflicts(ctx context.Context) ([]Conflict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.computeConflictsLocked(ctx)
}

func (s *Service) computeConflictsLocked(ctx context.Context) ([]Conflict, error) {
	st, err := s.Store.LoadState()
	if err != nil {
		return nil, err
	}
	sd, err := s.readServer(ctx)
	if err != nil {
		if errors.Is(err, caddy.ErrNotFound) {
			return s.missingContextConflicts()
		}
		return nil, err
	}
	j, err := s.Store.LoadJournal()
	if err != nil {
		return nil, err
	}
	dup := model.DuplicateIDs(sd.Routes)
	out := []Conflict{}
	for id, e := range st.Routes {
		if id == SelfID {
			continue
		}
		cur, _ := findRoute(sd.Routes, id)
		c := Conflict{RouteID: id, Host: firstOr(e.Snapshot.Hosts(), ""), Desired: clonePtr(&e.Snapshot), Blocked: map[string]model.Reason{}}
		unclear := j != nil && j.RouteID == id && j.Phase == state.PhaseUnclear
		switch {
		case unclear:
			c.Kind = "OP_UNCLEAR"
			c.Reason = "an interrupted operation must be resolved"
			if cur == nil {
				c.Blocked["take-current"] = model.Reason{Code: BlockRouteMissing, Message: "route does not exist"}
			}
		case dup[id] > 1:
			c.Kind = "DUP_ID"
			c.Reason = "@id occurs more than once in Caddy"
			c.Blocked["take-current"] = model.Reason{Code: BlockDupID, Message: "duplicate @id"}
			c.Blocked["apply-desired"] = model.Reason{Code: BlockDupID, Message: "duplicate @id"}
		case cur == nil:
			if idx := earlierTerminal(sd.Routes, e); idx >= 0 {
				c.Kind = "FOREIGN_COLLISION"
				c.Reason = fmt.Sprintf("earlier terminal route %q shadows the desired route", describeRoute(sd.Routes[idx]))
				c.Blocked["take-current"] = model.Reason{Code: BlockRouteMissing, Message: "route does not exist"}
				c.Blocked["apply-desired"] = model.Reason{Code: BlockForeign, Message: "blocked by a foreign collision"}
			} else {
				continue // MISSING_RESTOREABLE, handled automatically
			}
		default:
			if model.Equal(e.Snapshot, *cur) {
				continue
			}
			c.Kind = "EXTERNALLY_MODIFIED"
			c.Reason = "Caddy route differs from the desired state"
		}
		c.Current = clonePtr(cur)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RouteID < out[j].RouteID })
	s.conflicts = map[string]Conflict{}
	for _, c := range out {
		s.conflicts[c.RouteID] = c
	}
	return out, nil
}

// missingContextConflicts reports every managed route when the selected
// Caddy server no longer exists (spec §5.1, MISSING_CONTEXT).
func (s *Service) missingContextConflicts() ([]Conflict, error) {
	st, err := s.Store.LoadState()
	if err != nil {
		return nil, err
	}
	out := []Conflict{}
	for id, e := range st.Routes {
		if id == SelfID {
			continue
		}
		cur := clonePtr(&e.Snapshot)
		out = append(out, Conflict{
			RouteID: id,
			Kind:    "MISSING_CONTEXT",
			Host:    firstOr(e.Snapshot.Hosts(), ""),
			Reason:  fmt.Sprintf("Caddy server %q does not exist", s.Server),
			Blocked: map[string]model.Reason{
				"take-current":  {Code: BlockRouteMissing, Message: "route cannot be read"},
				"apply-desired": {Code: BlockMissingContext, Message: "selected Caddy server is missing"},
			},
			Desired: cur,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RouteID < out[j].RouteID })
	return out, nil
}

func earlierTerminal(routes []model.Route, e state.Entry) int {
	idx := e.Anchor.Index
	if idx < 0 {
		idx = 0
	}
	if idx > len(routes) {
		idx = len(routes)
	}
	for i := 0; i < idx; i++ {
		if routes[i].ID() == SelfID || !routes[i].Terminal() {
			continue
		}
		if model.RoutesCollide(routes[i], e.Snapshot) {
			return i
		}
	}
	return -1
}

// PreviewResolve builds the preview for one of the three conflict actions.
func (s *Service) PreviewResolve(ctx context.Context, sel, action string) (*Preview, error) {
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
	if !t.Managed || t.Entry == nil {
		return nil, fmt.Errorf("%w: route is not managed", ErrNotFound)
	}
	conflicts, err := s.computeConflictsLocked(ctx)
	if err != nil {
		return nil, err
	}
	var c *Conflict
	for i := range conflicts {
		if conflicts[i].RouteID == t.ID {
			c = &conflicts[i]
			break
		}
	}
	if c == nil {
		return nil, fmt.Errorf("%w: route %s has no conflict", ErrNotFound, sel)
	}
	p := &Preview{RouteID: t.ID, ETag: sd.ETag, Index: t.Index}
	if reason, blocked := c.Blocked[action]; blocked {
		switch action {
		case "take-current", "apply-desired", "unmanage":
			return blockedPreview(p, reason), nil
		default:
			return nil, fmt.Errorf("%w: unknown action %q", ErrInvalid, action)
		}
	}
	entry := *t.Entry
	switch action {
	case "take-current":
		p.Kind = KindTakeCurrent
		p.LocalOnly = true
		if t.Route == nil {
			return blockedPreview(p, model.Reason{Code: BlockRouteMissing, Message: "route does not exist"}), nil
		}
		oldSnapshot := entry.Snapshot.Clone()
		cur := t.Route.Clone()
		entry.Snapshot = cur
		entry.Anchor = state.Anchor{Index: t.Index}
		p.Base = clonePtr(t.Route)
		p.Route = clonePtr(&cur)
		p.Entry = &entry
		p.Index = t.Index
		p.Diff = s.redact(model.DiffRoute(&oldSnapshot, t.Route, t.Index))
		p.Description = fmt.Sprintf("take current Caddy state for %s", describeRoute(*t.Route))
	case "apply-desired":
		p.Kind = KindApplyDesired
		desired := entry.Snapshot.Clone()
		p.Route = &desired
		p.Base = clonePtr(t.Route)
		p.Entry = &entry
		if t.Route == nil {
			p.Index = entry.Anchor.Index
			if p.Index < 0 {
				p.Index = 0
			}
			if p.Index > len(sd.Routes) {
				p.Index = len(sd.Routes)
			}
			if idx := earlierTerminal(sd.Routes, entry); idx >= 0 {
				return blockedPreview(p, model.Reason{Code: BlockForeign, Message: fmt.Sprintf("earlier terminal route %q blocks the restore", describeRoute(sd.Routes[idx]))}), nil
			}
			p.Diff = s.redact(model.DiffRoute(nil, &desired, p.Index))
		} else {
			p.Index = t.Index
			if i := collideWith(desired, sd.Routes, t.Index); i >= 0 {
				return blockedPreview(p, model.Reason{Code: BlockForeign, Message: fmt.Sprintf("collides with route %q", describeRoute(sd.Routes[i]))}), nil
			}
			p.Diff = s.redact(model.DiffRoute(t.Route, &desired, t.Index))
		}
		p.Description = fmt.Sprintf("apply desired state for %s", firstOr(desired.Hosts(), entry.ID))
	case "unmanage":
		p.Kind = KindUnmanage
		p.LocalOnly = true
		p.Base = clonePtr(t.Route)
		p.RemoveEntry = true
		p.Entry = nil
		p.Diff = []model.Change{{Op: "remove", Path: "/state/routes/" + t.ID, Before: entry.Snapshot}}
		p.Description = "unmanage route; Caddy configuration stays unchanged"
	default:
		return nil, fmt.Errorf("%w: unknown action %q", ErrInvalid, action)
	}
	return p, nil
}

func blockedPreview(p *Preview, reason model.Reason) *Preview {
	p.Blocked = &reason
	return p
}

// Reconcile restores safe missing routes and records conflicts.
func (s *Service) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastReconcile = time.Now()
	st, err := s.Store.LoadState()
	if err != nil {
		s.lastCaddyErr = err.Error()
		return err
	}
	sd, err := s.readServer(ctx)
	if err != nil {
		s.lastCaddyErr = err.Error()
		return err
	}
	s.lastCaddyErr = ""
	if _, err := s.computeConflictsLocked(ctx); err != nil {
		return err
	}
	j, _ := s.Store.LoadJournal()
	dup := model.DuplicateIDs(sd.Routes)
	for id, e := range st.Routes {
		if id == SelfID {
			continue
		}
		if j != nil && j.RouteID == id {
			continue
		}
		if dup[id] > 0 {
			continue
		}
		if _, idx := findRoute(sd.Routes, id); idx >= 0 {
			continue
		}
		if earlierTerminal(sd.Routes, e) >= 0 {
			continue
		}
		desired := e.Snapshot.Clone()
		entry := e
		o := op{
			kind:        KindRestore,
			routeID:     id,
			desired:     &desired,
			entry:       &entry,
			index:       e.Anchor.Index,
			touchUndo:   false,
			description: "restore route " + firstOr(e.Snapshot.Hosts(), id),
			diff:        s.redact(model.DiffRoute(nil, &desired, e.Anchor.Index)),
		}
		if o.index < 0 {
			o.index = 0
		}
		if o.index > len(sd.Routes) {
			o.index = len(sd.Routes)
		}
		if _, err := s.executeLocked(ctx, o); err != nil {
			s.conflicts[id] = Conflict{RouteID: id, Kind: "RESTORE_FAILED", Host: firstOr(e.Snapshot.Hosts(), ""), Reason: err.Error()}
			continue
		}
	}
	return nil
}

// RunReconcile runs the loop with jitter and backoff (web process only).
func (s *Service) RunReconcile(ctx context.Context, interval time.Duration, stop <-chan struct{}) {
	backoff := interval
	run := func() bool {
		err := s.Reconcile(ctx)
		if err != nil {
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			s.logf(logging.Warn, "reconcile failed: %v (next attempt in up to %s)", err, backoff)
			return false
		}
		backoff = interval
		return true
	}
	failing := !run()
	for {
		d := jitter(backoff)
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-time.After(d):
			ok := run()
			if ok && failing {
				// API is back: reconcile immediately instead of waiting a
				// full interval (spec §5.1).
				s.logf(logging.Info, "reconcile recovered")
				run()
			}
			failing = !ok
		}
	}
}

func jitter(d time.Duration) time.Duration {
	// ±20% jitter without a global RNG dependency.
	span := 2*(d/5) + 1
	n := time.Now().UnixNano() % int64(span)
	return d + time.Duration(n) - d/5
}
