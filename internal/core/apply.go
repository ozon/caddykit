package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"caddykit/internal/caddy"
	"caddykit/internal/logging"
	"caddykit/internal/model"
	"caddykit/internal/state"
	"github.com/google/uuid"
)

type op struct {
	kind        Kind
	routeID     string
	base        *model.Route
	desired     *model.Route
	entry       *state.Entry
	removeEntry bool
	index       int
	localOnly   bool
	touchUndo   bool
	clearUndo   bool
	description string
	diff        []model.Change
}

func newOpID() string { return uuid.Must(uuid.NewV7()).String() }

// Apply confirms and executes a preview.
func (s *Service) Apply(ctx context.Context, p *Preview) (res *Result, err error) {
	description := ""
	if p != nil {
		description = p.Description
	}
	defer func() { s.logMutation("apply", description, res, err) }()
	s.mu.Lock()
	defer s.mu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("%w: empty preview", ErrInvalid)
	}
	if p.Blocked != nil {
		return nil, &BlockedError{Reason: *p.Blocked}
	}
	switch p.Kind {
	case KindCreate, KindEdit, KindDelete, KindAdopt, KindUnmanage, KindTakeCurrent, KindApplyDesired:
	default:
		return nil, fmt.Errorf("%w: unsupported operation %q", ErrInvalid, p.Kind)
	}
	o := op{
		kind: p.Kind, routeID: p.RouteID, base: p.Base, desired: p.Route,
		entry: p.Entry, removeEntry: p.RemoveEntry, index: p.Index,
		localOnly: p.LocalOnly, touchUndo: true, description: p.Description, diff: p.Diff,
	}
	return s.executeLocked(ctx, o)
}

func (s *Service) executeLocked(ctx context.Context, o op) (*Result, error) {
	oldJournal, err := s.Store.LoadJournal()
	if err != nil {
		return nil, err
	}
	if oldJournal != nil {
		// shortcut: the journal has one slot; an unclear operation blocks all
		// mutations until it is resolved, not only its own route.
		if oldJournal.Phase != state.PhaseUnclear || oldJournal.RouteID != o.routeID {
			return nil, ErrJournalOpen
		}
		switch o.kind {
		case KindTakeCurrent, KindApplyDesired, KindUnmanage, KindRestore:
			if err := s.Store.ClearJournal(); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%w: route has an unclear operation", ErrJournalOpen)
		}
	}

	st, err := s.Store.LoadState()
	if err != nil {
		return nil, err
	}
	sd, err := s.readServer(ctx)
	if err != nil {
		return nil, err
	}

	cur, idx := locate(sd.Routes, o)
	if !sameRoute(cur, o.base) {
		return nil, fmt.Errorf("%w: route changed since the preview", ErrStale)
	}
	if o.kind == KindCreate && idx >= 0 {
		return nil, fmt.Errorf("%w: @id already exists", ErrStale)
	}
	if o.desired != nil && o.desired.ID() != o.routeID {
		// Undoing an adoption that assigned an @id removes the identity again.
		detach := o.kind == KindUndo && o.removeEntry && o.desired.ID() == "" && o.base != nil && o.base.ID() == o.routeID
		if !detach {
			return nil, fmt.Errorf("%w: @id mismatch", ErrInvalid)
		}
	}
	if o.routeID == SelfID && o.kind != KindRestore {
		return nil, ErrSelfProtected
	}
	if o.desired != nil && model.HasInsecureSkipVerify(*o.desired) && !s.AllowInsecure {
		return nil, fmt.Errorf("%w: insecure_skip_verify requires --allow-insecure", ErrInvalid)
	}

	build := func(routes []model.Route) ([]model.Route, int, error) {
		return applyToList(routes, o, idx)
	}
	newRoutes, actualIdx, err := build(sd.Routes)
	if err != nil {
		return nil, err
	}
	if berr := checkCollision(o, newRoutes, actualIdx); berr != nil {
		return nil, berr
	}

	entry := o.entry
	if entry != nil {
		e := *entry
		// The entry carries the authoritative desired snapshot. It can differ
		// from the Caddy route (e.g. undo of apply-desired restores the route
		// to the externally modified state but keeps the desired snapshot).
		e.Anchor.Index = actualIdx
		entry = &e
	}

	oldImg := &state.Image{Route: imageRoute(o.base), Entry: stateEntryPtr(st, o.routeID), Index: idx}
	newRoute := clonePtr(o.desired)
	if o.localOnly {
		newRoute = clonePtr(o.base)
	}
	newImg := &state.Image{Route: imageRoute(newRoute), Entry: entry, Index: actualIdx}

	var undo *state.Undo
	if o.touchUndo {
		undo = &state.Undo{
			OpID:        newOpID(),
			Kind:        string(o.kind),
			Description: o.description,
			CreatedAt:   time.Now(),
			Before:      oldImg,
			After:       newImg,
		}
	}
	j := &state.Journal{
		OpID:      newOpID(),
		Kind:      string(o.kind),
		RouteID:   o.routeID,
		Phase:     state.PhasePrepared,
		Old:       oldImg,
		New:       newImg,
		Undo:      undo,
		TouchUndo: o.touchUndo,
		ClearUndo: o.clearUndo,
		BaseETag:  sd.ETag,
		StartedAt: time.Now(),
	}
	if o.localOnly {
		j.Phase = state.PhaseCaddyWritten
	}
	if err := s.Store.SaveJournal(j); err != nil {
		return nil, err
	}

	if !o.localOnly {
		wrote := false
		for attempt := 1; attempt <= 3; attempt++ {
			if attempt > 1 {
				sd, err = s.readServer(ctx)
				if err != nil {
					return nil, err
				}
				cur, idx = locate(sd.Routes, o)
				if !sameRoute(cur, o.base) {
					_ = s.Store.ClearJournal()
					return nil, fmt.Errorf("%w: route changed during retry", ErrStale)
				}
				newRoutes, actualIdx, err = build(sd.Routes)
				if err != nil {
					_ = s.Store.ClearJournal()
					return nil, err
				}
				if berr := checkCollision(o, newRoutes, actualIdx); berr != nil {
					_ = s.Store.ClearJournal()
					return nil, berr
				}
			}
			err = s.writeRoutes(ctx, newRoutes, sd.ETag, sd.HasRoutes)
			if errors.Is(err, caddy.ErrPreconditionFailed) {
				continue
			}
			if err != nil {
				if IsCaddyUnreachable(err) {
					return nil, fmt.Errorf("caddy write failed; journal kept for recovery: %w", err)
				}
				_ = s.Store.ClearJournal()
				return nil, err
			}
			wrote = true
			break
		}
		if !wrote {
			_ = s.Store.ClearJournal()
			return nil, fmt.Errorf("%w: concurrent modifications exceeded 3 attempts", ErrStale)
		}
		after, err := s.readServer(ctx)
		if err != nil {
			return nil, fmt.Errorf("caddy written but verification read failed; journal kept: %w", err)
		}
		if !verifyRouteAt(after.Routes, actualIdx, o.routeID, o.desired) {
			j.Phase = state.PhaseUnclear
			j.Error = "route differs from the expected state after write"
			_ = s.Store.SaveJournal(j)
			return nil, fmt.Errorf("caddy verification failed; route marked %s", BlockOpUnclear)
		}
		j.Phase = state.PhaseCaddyWritten
		j.New.Index = actualIdx
		if j.New.Entry != nil {
			e := *j.New.Entry
			e.Anchor.Index = actualIdx
			if j.Undo != nil && j.Undo.After != nil {
				j.Undo.After.Index = actualIdx
				j.Undo.After.Entry = &e
			}
			j.New.Entry = &e
		}
		if err := s.Store.SaveJournal(j); err != nil {
			return nil, err
		}
	}

	if err := s.commitLocal(j); err != nil {
		return nil, fmt.Errorf("caddy updated but local commit failed; journal kept: %w", err)
	}
	diff := o.diff
	if diff == nil {
		diff = model.DiffRoute(o.base, o.desired, actualIdx)
	}
	return &Result{
		Kind:    o.kind,
		RouteID: o.routeID,
		Route:   clonePtr(o.desired),
		Entry:   entry,
		Diff:    s.redact(diff),
		Message: o.description,
	}, nil
}

func applyToList(routes []model.Route, o op, curIdx int) ([]model.Route, int, error) {
	if o.localOnly {
		if curIdx < 0 {
			curIdx = 0
		}
		return routes, curIdx, nil
	}
	out := make([]model.Route, len(routes))
	copy(out, routes)
	if o.desired == nil {
		if curIdx < 0 {
			return nil, 0, fmt.Errorf("%w: route %s is missing", ErrStale, o.routeID)
		}
		out = append(out[:curIdx], out[curIdx+1:]...)
		return out, curIdx, nil
	}
	if o.base == nil {
		idx := o.index
		if idx < 0 {
			idx = 0
		}
		if idx > len(out) {
			idx = len(out)
		}
		out = append(out, nil)
		copy(out[idx+1:], out[idx:])
		out[idx] = *o.desired
		return out, idx, nil
	}
	if curIdx < 0 {
		return nil, 0, fmt.Errorf("%w: route %s is missing", ErrStale, o.routeID)
	}
	out[curIdx] = *o.desired
	return out, curIdx, nil
}

// locate finds the current route by @id, falling back to the preview index
// when the operation assigns an @id to a route that had none.
func locate(routes []model.Route, o op) (*model.Route, int) {
	if o.routeID != "" {
		if i := indexOfID(routes, o.routeID); i >= 0 {
			return &routes[i], i
		}
	}
	if o.base != nil && o.index >= 0 && o.index < len(routes) && model.Equal(routes[o.index], *o.base) {
		return &routes[o.index], o.index
	}
	return nil, -1
}

func findRoute(routes []model.Route, id string) (*model.Route, int) {
	if id == "" {
		return nil, -1
	}
	idx := indexOfID(routes, id)
	if idx < 0 {
		return nil, -1
	}
	return &routes[idx], idx
}

func indexOfID(routes []model.Route, id string) int {
	for i := range routes {
		if routes[i].ID() == id {
			return i
		}
	}
	return -1
}

func sameRoute(cur, base *model.Route) bool {
	if cur == nil || base == nil {
		return cur == nil && base == nil
	}
	return model.Equal(*cur, *base)
}

func verifyRoute(routes []model.Route, id string, desired *model.Route) bool {
	cur, _ := findRoute(routes, id)
	return sameRoute(cur, desired)
}

// verifyRouteAt handles operations that remove an @id (undo of an adoption
// that assigned one): the route is no longer reachable by its old identity.
func verifyRouteAt(routes []model.Route, idx int, id string, desired *model.Route) bool {
	if desired == nil || desired.ID() != "" {
		return verifyRoute(routes, id, desired)
	}
	if idx >= 0 && idx < len(routes) && model.Equal(routes[idx], *desired) {
		return true
	}
	for i := range routes {
		if model.Equal(routes[i], *desired) {
			return true
		}
	}
	return false
}

func stateEntryPtr(st *state.State, id string) *state.Entry {
	if e, ok := st.Routes[id]; ok {
		c := e
		return &c
	}
	return nil
}

func (s *Service) commitLocal(j *state.Journal) error {
	return s.Store.WithStateLock(func() error {
		st, err := s.Store.LoadState()
		if err != nil {
			return err
		}
		if st.Server == "" {
			st.Server = s.Server
		}
		if j.New != nil && j.New.Entry != nil {
			st.Routes[j.New.Entry.ID] = *j.New.Entry
		} else {
			delete(st.Routes, j.RouteID)
		}
		if err := s.Store.SaveState(st); err != nil {
			return err
		}
		if j.ClearUndo {
			if err := s.Store.ClearUndo(); err != nil {
				return err
			}
		} else if j.TouchUndo && j.Undo != nil {
			if err := s.Store.SaveUndo(j.Undo); err != nil {
				return err
			}
		}
		return s.Store.ClearJournal()
	})
}

// Recover resolves an interrupted operation at startup (spec §4.3).
func (s *Service) Recover(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.Store.LoadJournal()
	if err != nil || j == nil {
		return err
	}
	if j.Phase == state.PhaseUnclear {
		return nil
	}
	if j.LocalOnly {
		return s.commitLocal(j)
	}
	sd, err := s.readServer(ctx)
	if err != nil {
		return err
	}
	oldMatch := imageMatchesRoutes(sd.Routes, j.Old, j.RouteID)
	newMatch := imageMatchesRoutes(sd.Routes, j.New, j.RouteID)
	switch {
	case j.Phase == state.PhasePrepared && oldMatch && !newMatch:
		s.logf(logging.Info, "recovered: dropped prepared journal for route %s", j.RouteID)
		return s.Store.ClearJournal()
	case newMatch:
		s.logf(logging.Info, "recovered: committed journal for route %s (phase %s)", j.RouteID, j.Phase)
		return s.commitLocal(j)
	case j.Phase == state.PhasePrepared && oldMatch && newMatch:
		s.logf(logging.Info, "recovered: dropped prepared journal for route %s (caddy unchanged)", j.RouteID)
		return s.Store.ClearJournal()
	default:
		j.Phase = state.PhaseUnclear
		j.Error = "Caddy state matches neither the before nor the after image"
		s.logf(logging.Warn, "recovery unclear for route %s: %s", j.RouteID, j.Error)
		if err := s.Store.SaveJournal(j); err != nil {
			return err
		}
		// Startup continues: the conflict center exposes OP_UNCLEAR and lets
		// the user take-current, apply-desired or unmanage.
		return nil
	}
}

func imageMatchesRoutes(routes []model.Route, img *state.Image, routeID string) bool {
	if img == nil {
		return true
	}
	if img.Route == nil {
		return indexOfID(routes, routeID) < 0
	}
	if id := img.Route.ID(); id != "" {
		if i := indexOfID(routes, id); i >= 0 {
			return model.Equal(routes[i], img.Route)
		}
	}
	if img.Index >= 0 && img.Index < len(routes) && model.Equal(routes[img.Index], img.Route) {
		return true
	}
	for i := range routes {
		if model.Equal(routes[i], img.Route) {
			return true
		}
	}
	return false
}

func imageRoute(p *model.Route) model.Route {
	if p == nil {
		return nil
	}
	return (*p).Clone()
}

func imagePtr(r model.Route) *model.Route {
	if r == nil {
		return nil
	}
	c := r.Clone()
	return &c
}

// UndoInfo returns the single persisted undo step.
func (s *Service) UndoInfo() (*state.Undo, error) {
	return s.Store.LoadUndo()
}

// Undo reverts the last user operation after verifying the after image.
func (s *Service) Undo(ctx context.Context) (res *Result, err error) {
	description := ""
	defer func() { s.logMutation("undo", description, res, err) }()
	s.mu.Lock()
	defer s.mu.Unlock()
	u, err := s.Store.LoadUndo()
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrNoUndo
	}
	description = "undo: " + u.Description
	if j, _ := s.Store.LoadJournal(); j != nil {
		return nil, ErrJournalOpen
	}
	st, err := s.Store.LoadState()
	if err != nil {
		return nil, err
	}
	sd, err := s.readServer(ctx)
	if err != nil {
		return nil, err
	}
	id := undoRouteID(u)
	cur, idx := findRoute(sd.Routes, id)
	if u.After == nil || !sameRoute(cur, imagePtr(u.After.Route)) {
		return nil, fmt.Errorf("%w: Caddy state changed after the operation", ErrUndoBlocked)
	}
	if u.After.Entry == nil {
		if _, ok := st.Routes[id]; ok {
			return nil, fmt.Errorf("%w: local state changed after the operation", ErrUndoBlocked)
		}
	} else {
		e, ok := st.Routes[id]
		if !ok || !model.Equal(e.Snapshot, u.After.Entry.Snapshot) || e.Source != u.After.Entry.Source {
			return nil, fmt.Errorf("%w: local state changed after the operation", ErrUndoBlocked)
		}
	}
	if u.Before == nil {
		return nil, fmt.Errorf("%w: undo image is incomplete", ErrUndoBlocked)
	}
	o := op{
		kind:        KindUndo,
		routeID:     id,
		base:        clonePtr(cur),
		desired:     imagePtr(u.Before.Route),
		entry:       u.Before.Entry,
		removeEntry: u.Before.Entry == nil,
		index:       idx,
		touchUndo:   false,
		clearUndo:   true,
		description: "undo: " + u.Description,
	}
	if o.desired != nil && o.base != nil && model.Equal(*o.desired, *o.base) {
		o.localOnly = true
	}
	return s.executeLocked(ctx, o)
}

func undoRouteID(u *state.Undo) string {
	if u.After != nil {
		if u.After.Entry != nil {
			return u.After.Entry.ID
		}
		if u.After.Route != nil {
			return u.After.Route.ID()
		}
	}
	if u.Before != nil {
		if u.Before.Entry != nil {
			return u.Before.Entry.ID
		}
		if u.Before.Route != nil {
			return u.Before.Route.ID()
		}
	}
	return ""
}

func (s *Service) logMutation(caller, description string, res *Result, err error) {
	if err != nil {
		if description != "" {
			s.logf(logging.Error, "%s failed: %s: %v", caller, description, err)
		} else {
			s.logf(logging.Error, "%s failed: %v", caller, err)
		}
		return
	}
	if res == nil {
		return
	}
	s.logf(logging.Info, "%s ok: kind=%s route=%s %s", caller, res.Kind, res.RouteID, res.Message)
}
