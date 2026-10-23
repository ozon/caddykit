package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"caddykit/internal/core"
	"caddykit/internal/model"
	"caddykit/internal/render"
	"caddykit/internal/state"
	"caddykit/internal/templates"
)

type ctxKey int

const ctxSession ctxKey = 1

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error       string   `json:"error"`
	Code        string   `json:"code,omitempty"`
	BlockReason string   `json:"block_reason,omitempty"`
	Candidates  []string `json:"candidates,omitempty"`
	RetryAfter  int      `json:"retry_after,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, msg, block string, candidates []string) {
	writeJSON(w, status, errorBody{Error: msg, Code: code, BlockReason: block, Candidates: candidates})
}

func (s *Server) writeCoreError(w http.ResponseWriter, err error) {
	var be *core.BlockedError
	var ae *core.AmbiguousError
	var rl *RateLimitError
	switch {
	case errors.Is(err, ErrBadPassword):
		writeError(w, http.StatusUnauthorized, "INVALID_PASSWORD", "invalid password", "", nil)
	case errors.As(err, &rl):
		w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many login attempts", "", nil)
	case errors.As(err, &be):
		writeError(w, http.StatusConflict, "BLOCKED", be.Reason.Message, be.Reason.Code, nil)
	case errors.As(err, &ae):
		writeError(w, http.StatusConflict, "AMBIGUOUS", ae.Error(), "", ae.Candidates)
	case errors.Is(err, core.ErrStale):
		writeError(w, http.StatusConflict, "PREVIEW_STALE", err.Error(), core.BlockStale, nil)
	case errors.Is(err, core.ErrJournalOpen):
		writeError(w, http.StatusConflict, "JOURNAL_OPEN", err.Error(), core.BlockOpUnclear, nil)
	case errors.Is(err, core.ErrUndoBlocked):
		writeError(w, http.StatusConflict, "UNDO_BLOCKED", err.Error(), core.BlockUndoMismatch, nil)
	case errors.Is(err, core.ErrNoUndo):
		writeError(w, http.StatusConflict, "NO_UNDO", err.Error(), core.BlockNoUndo, nil)
	case errors.Is(err, core.ErrSelfProtected):
		writeError(w, http.StatusConflict, "SELF_PROTECTED", err.Error(), core.BlockSelf, nil)
	case errors.Is(err, core.ErrInvalid):
		writeError(w, http.StatusBadRequest, "INVALID", err.Error(), "", nil)
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), "", nil)
	case core.IsCaddyUnreachable(err):
		writeError(w, http.StatusBadGateway, "CADDY_UNREACHABLE", err.Error(), "", nil)
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), "", nil)
	}
}

// guard authenticates API calls. IPC calls use X-Devproxy-Token; browser
// calls use the session cookie, and writes additionally require the CSRF
// header. While draining all API calls return 503.
func (s *Server) guard(write bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.draining.Load() {
			writeError(w, http.StatusServiceUnavailable, "DRAINING", "caddykit is shutting down", "", nil)
			return
		}
		tok := r.Header.Get("X-Devproxy-Token")
		if tok != "" {
			if !constantEqual(tok, s.token) {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid IPC token", "", nil)
				return
			}
			h(w, r)
			return
		}
		c, err := r.Cookie("caddykit_session")
		if err != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", "", nil)
			return
		}
		sess, ok := s.auth.Validate(c.Value)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "session expired", "", nil)
			return
		}
		if write {
			hdr := r.Header.Get("X-CSRF-Token")
			if hdr == "" || !constantEqual(hdr, sess.CSRF) {
				writeError(w, http.StatusForbidden, "CSRF", "missing or invalid CSRF token", "", nil)
				return
			}
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxSession, sess.Token)))
	}
}

func (s *Server) api(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/login", s.login)
	mux.HandleFunc("GET /api/v1/session", s.guard(false, s.sessionInfo))
	mux.HandleFunc("POST /api/v1/logout", s.guard(true, s.logout))
	mux.HandleFunc("POST /api/v1/auth/rotate-password", s.guard(true, s.rotatePassword))
	mux.HandleFunc("GET /api/v1/routes", s.guard(false, s.listRoutes))
	mux.HandleFunc("GET /api/v1/routes/{id...}", s.guard(false, s.showRoute))
	mux.HandleFunc("POST /api/v1/routes/preview", s.guard(true, s.preview))
	mux.HandleFunc("POST /api/v1/routes/apply", s.guard(true, s.apply))
	mux.HandleFunc("GET /api/v1/conflicts", s.guard(false, s.conflicts))
	mux.HandleFunc("POST /api/v1/conflicts/resolve", s.guard(true, s.resolve))
	mux.HandleFunc("GET /api/v1/undo", s.guard(false, s.undoInfo))
	mux.HandleFunc("POST /api/v1/undo", s.guard(true, s.undo))
	mux.HandleFunc("GET /api/v1/templates", s.guard(false, s.listTemplates))
	mux.HandleFunc("GET /api/v1/templates/{name}", s.guard(false, s.showTemplate))
	mux.HandleFunc("POST /api/v1/render/caddyfile", s.guard(false, s.renderCaddyfile))
	mux.HandleFunc("GET /api/v1/status", s.guard(false, s.status))
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", err.Error(), "", nil)
		return
	}
	tok, csrf, err := s.auth.Login(clientIP(r), req.Password)
	if err != nil {
		var rl *RateLimitError
		if errors.As(err, &rl) {
			s.opts.Logger.Printf("login rate-limited for %s (retry after %s)", clientIP(r), rl.RetryAfter.Round(time.Second))
		} else {
			// The password itself is never logged (auth.go).
			s.opts.Logger.Printf("login failed for %s: %v", clientIP(r), err)
		}
		s.writeCoreError(w, err)
		return
	}
	s.opts.Logger.Printf("login succeeded for %s", clientIP(r))
	http.SetCookie(w, &http.Cookie{
		Name: "caddykit_session", Value: tok, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: "caddykit_csrf", Value: csrf, Path: "/",
		Secure: true, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"csrf": csrf, "domain": s.opts.Domain})
}

func (s *Server) sessionInfo(w http.ResponseWriter, r *http.Request) {
	tok, _ := r.Context().Value(ctxSession).(string)
	csrf, _ := s.auth.CSRF(tok)
	writeJSON(w, http.StatusOK, map[string]any{"csrf": csrf, "domain": s.opts.Domain, "version": s.opts.Version})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if tok, ok := r.Context().Value(ctxSession).(string); ok {
		s.auth.Logout(tok)
	}
	clearCookie(w, "caddykit_session")
	clearCookie(w, "caddykit_csrf")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Secure: true, SameSite: http.SameSiteStrictMode, HttpOnly: name == "caddykit_session"})
}

func (s *Server) rotatePassword(w http.ResponseWriter, r *http.Request) {
	tok, _ := r.Context().Value(ctxSession).(string)
	pw, err := s.auth.RotatePassword(tok)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), "", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"password": pw})
}

func (s *Server) listRoutes(w http.ResponseWriter, r *http.Request) {
	if id := r.URL.Query().Get("id"); id != "" {
		v, err := s.svc.Show(r.Context(), id)
		if err != nil {
			s.writeCoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
	all := r.URL.Query().Get("all") != "0"
	list, err := s.svc.List(r.Context(), all)
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routes": list})
}

func (s *Server) showRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "INVALID", "missing route id", "", nil)
		return
	}
	v, err := s.svc.Show(r.Context(), id)
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type previewRequest struct {
	Kind     string            `json:"kind"`
	ID       string            `json:"id"`
	Spec     *model.Spec       `json:"spec"`
	Route    json.RawMessage   `json:"route"`
	Template string            `json:"template"`
	Params   map[string]string `json:"params"`
}

type previewResponse struct {
	*core.Preview
	Requirements []string `json:"requirements,omitempty"`
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", err.Error(), "", nil)
		return
	}
	ctx := r.Context()
	var p *core.Preview
	var err error
	var requirements []string
	var sensitive []string
	switch core.Kind(req.Kind) {
	case core.KindCreate:
		var sp model.Spec
		if len(req.Route) > 0 && string(req.Route) != "null" {
			raw, derr := model.Decode(req.Route)
			if derr != nil {
				writeError(w, http.StatusBadRequest, "INVALID", derr.Error(), "", nil)
				return
			}
			p, err = s.svc.PreviewCreateJSON(ctx, raw, "create")
		} else if req.Template != "" {
			tmpl, terr := templates.Get(s.opts.TemplateDir, req.Template)
			if terr != nil {
				s.writeCoreError(w, terr)
				return
			}
			sp, requirements, err = tmpl.Render(req.Params)
			if err != nil {
				writeError(w, http.StatusBadRequest, "INVALID", err.Error(), "", nil)
				return
			}
			sensitive = tmpl.SensitiveValues(req.Params)
			p, err = s.svc.PreviewCreate(ctx, sp, "template", &state.TemplateRef{Name: tmpl.ID, Version: "1"})
		} else {
			if req.Spec == nil {
				writeError(w, http.StatusBadRequest, "INVALID", "spec or route or template is required", "", nil)
				return
			}
			p, err = s.svc.PreviewCreate(ctx, *req.Spec, "create", nil)
		}
	case core.KindEdit:
		if len(req.Route) > 0 {
			raw, derr := model.Decode(req.Route)
			if derr != nil {
				writeError(w, http.StatusBadRequest, "INVALID", derr.Error(), "", nil)
				return
			}
			p, err = s.svc.PreviewEditJSON(ctx, req.ID, raw)
		} else {
			if req.Spec == nil {
				writeError(w, http.StatusBadRequest, "INVALID", "spec or route is required", "", nil)
				return
			}
			p, err = s.svc.PreviewEditForm(ctx, req.ID, *req.Spec)
		}
	case core.KindDelete:
		p, err = s.svc.PreviewDelete(ctx, req.ID)
	case core.KindAdopt:
		p, err = s.svc.PreviewAdopt(ctx, req.ID)
	case core.KindTakeCurrent, core.KindApplyDesired:
		p, err = s.svc.PreviewResolve(ctx, req.ID, string(req.Kind))
	case core.KindUnmanage:
		p, err = s.svc.PreviewUnmanage(ctx, req.ID)
	default:
		writeError(w, http.StatusBadRequest, "INVALID", "unknown preview kind", "", nil)
		return
	}
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	if len(sensitive) > 0 {
		p.Diff = model.RedactValues(p.Diff, sensitive)
		p.Description = model.RedactText(p.Description, sensitive)
		for i := range p.Warnings {
			p.Warnings[i] = model.RedactText(p.Warnings[i], sensitive)
		}
		for i := range requirements {
			requirements[i] = model.RedactText(requirements[i], sensitive)
		}
	}
	writeJSON(w, http.StatusOK, previewResponse{Preview: p, Requirements: requirements})
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	var p core.Preview
	if err := decodeBody(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", err.Error(), "", nil)
		return
	}
	res, err := s.svc.Apply(r.Context(), &p)
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) conflicts(w http.ResponseWriter, r *http.Request) {
	cs, err := s.svc.Conflicts(r.Context())
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": cs})
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", err.Error(), "", nil)
		return
	}
	p, err := s.svc.PreviewResolve(r.Context(), req.ID, req.Action)
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, previewResponse{Preview: p})
}

func (s *Server) undoInfo(w http.ResponseWriter, r *http.Request) {
	u, err := s.svc.UndoInfo()
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	if u == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": true, "kind": u.Kind, "description": u.Description, "created_at": u.CreatedAt,
	})
}

func (s *Server) undo(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.Undo(r.Context())
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := templates.Load(s.opts.TemplateDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TEMPLATES", err.Error(), "", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": list})
}

func (s *Server) showTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	t, err := templates.Get(s.opts.TemplateDir, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), "", nil)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) renderCaddyfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Route json.RawMessage `json:"route"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", err.Error(), "", nil)
		return
	}
	if len(req.Route) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID", "route is required", "", nil)
		return
	}
	raw, err := model.Decode(req.Route)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID", err.Error(), "", nil)
		return
	}
	text, warnings := render.Route(raw)
	writeJSON(w, http.StatusOK, map[string]any{"caddyfile": text, "warnings": warnings})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	st := s.svc.Status(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"caddy_ok":           st.CaddyOK,
		"caddy_error":        st.CaddyError,
		"server":             st.Server,
		"last_reconcile":     st.LastReconcile,
		"state_routes":       st.StateRoutes,
		"conflicts":          st.Conflicts,
		"undo_available":     st.UndoAvailable,
		"domain":             s.opts.Domain,
		"version":            s.opts.Version,
		"reconcile_interval": s.opts.ReconcileInterval.String(),
	})
}

func decodeBody(r *http.Request, v any) error {
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}
