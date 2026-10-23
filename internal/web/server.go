package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"caddykit/internal/caddy"
	"caddykit/internal/core"
	"caddykit/internal/model"
	"caddykit/internal/state"
)

// The all: prefix is required: SvelteKit puts assets in _app/, and plain
// the embed pattern ignores underscore-prefixed paths.
//
//go:embed all:dist
var distFS embed.FS

var ErrInstance = errors.New("another caddykit instance is running")

type Options struct {
	CaddyAdmin        string
	Domain            string
	Server            string
	DataDir           string
	TemplateDir       string
	ReconcileInterval time.Duration
	AllowInsecure     bool
	Version           string
	Out               io.Writer
	Logger            *log.Logger
}

type Server struct {
	opts  Options
	store *state.Store
	caddy *caddy.Client
	svc   *core.Service
	auth  *Auth
	token string

	httpSrv *http.Server
	uds     net.Listener
	udsPath string

	draining    atomic.Bool
	selfCreated bool
}

// Run starts the web process: locks the instance, checks Caddy, recovers the
// journal, creates/repairs the self route, serves UI+API+IPC and reconciles.
func Run(ctx context.Context, opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Logger == nil {
		opts.Logger = log.New(os.Stderr, "caddykit: ", log.LstdFlags)
	}
	opts.Logger.Printf("starting: data dir %s; caddy admin %s; server %q", opts.DataDir, opts.CaddyAdmin, opts.Server)
	if err := ensureDataDir(opts.DataDir); err != nil {
		return err
	}
	store, err := state.Open(opts.DataDir)
	if err != nil {
		return err
	}
	instance, err := state.TryLock(store.Path(state.FileInstance))
	if err != nil {
		if errors.Is(err, state.ErrLocked) {
			return ErrInstance
		}
		return err
	}
	defer func() { _ = instance.Close() }()

	cc, err := caddy.New(opts.CaddyAdmin)
	if err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := cc.Check(checkCtx); err != nil {
		return err
	}
	svc := core.New(cc, store, opts.Server)
	svc.AllowInsecure = opts.AllowInsecure
	svc.Logf = opts.Logger.Printf
	if err := svc.ValidateServer(checkCtx); err != nil {
		return err
	}
	if err := svc.Recover(checkCtx); err != nil {
		return err
	}
	auth, err := NewAuth()
	if err != nil {
		return err
	}
	token, err := store.Token()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	cleanupListener := func() { _ = ln.Close() }

	port := ln.Addr().(*net.TCPAddr).Port
	s := &Server{opts: opts, store: store, caddy: cc, svc: svc, auth: auth, token: token}
	auth.OnPassword = func(pw string) {
		_, _ = fmt.Fprintf(opts.Out, "\nNeues Startpasswort: %s\n", pw)
	}
	created, err := s.ensureSelfRoute(checkCtx, port)
	if err != nil {
		cleanupListener()
		return err
	}
	s.selfCreated = created
	if created {
		opts.Logger.Printf("created caddykit/self route for %s on 127.0.0.1:%d", opts.Domain, port)
	}
	cleanupAll := func() {
		cleanupListener()
		if s.uds != nil {
			_ = s.uds.Close()
			_ = os.Remove(s.udsPath)
		}
		if s.selfCreated {
			cctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.removeSelfRoute(cctx)
			c()
		}
	}

	udsPath := state.SocketPath(opts.DataDir)
	_ = os.Remove(udsPath)
	uds, err := net.Listen("unix", udsPath)
	if err != nil {
		cleanupAll()
		return err
	}
	if err := os.Chmod(udsPath, 0o600); err != nil {
		_ = uds.Close()
		cleanupAll()
		return err
	}
	s.uds, s.udsPath = uds, udsPath

	s.httpSrv = &http.Server{Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 2)
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	go func() {
		if err := serveUDS(uds, s.handler()); err != nil && !errors.Is(err, net.ErrClosed) {
			errCh <- err
		}
	}()

	opts.Logger.Printf("backend listening on %s", ln.Addr().String())
	_, _ = fmt.Fprintf(opts.Out, "caddykit web: https://%s\nStartpasswort: %s\n", opts.Domain, auth.Password())

	reconcileStop := make(chan struct{})
	reconcileDone := make(chan struct{})
	go func() {
		defer close(reconcileDone)
		svc.RunReconcile(ctx, opts.ReconcileInterval, reconcileStop)
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		close(reconcileStop)
		<-reconcileDone
		s.shutdown()
		cleanupAll()
		return err
	}
	close(reconcileStop)
	select {
	case <-reconcileDone:
	case <-time.After(5 * time.Second):
	}
	s.shutdown()
	return nil
}

func ensureDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

func serveUDS(ln net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) shutdown() {
	s.draining.Store(true)
	s.opts.Logger.Printf("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.httpSrv != nil {
		_ = s.httpSrv.Shutdown(ctx)
	}
	if s.uds != nil {
		_ = s.uds.Close()
		_ = os.Remove(s.udsPath)
	}
	// Own route becomes a static 503. Failure is only a warning, never
	// reported as success (spec §6.3).
	if err := s.setSelf503(ctx); err != nil {
		s.opts.Logger.Printf("warning: could not switch caddykit/self to 503: %v", err)
	}
	s.opts.Logger.Printf("shutdown complete")
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	s.api(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown API endpoint", "", nil)
	})
	mux.HandleFunc("/", s.serveSPA)
	return s.logRequests(securityHeaders(mux))
}

// statusRecorder captures the status code a handler chose so the request log
// can report it. It keeps the first WriteHeader, like net/http does.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logRequests writes one line per API call. Only the path is logged: query
// strings can carry values that must not end up in a log file.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.opts.Logger.Printf("%s %s %d %s %s", r.Method, r.URL.Path, rec.status,
			time.Since(start).Round(time.Millisecond), clientIP(r))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CSP for the SPA comes from SvelteKit's hashed <meta> tag because
		// adapter-static cannot inject a per-request nonce. A header without
		// those hashes would block the bootstrap script.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) serveSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown API endpoint", "", nil)
		return
	}
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		http.Error(w, "UI not built", http.StatusInternalServerError)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "200.html"
	}
	if st, err := fs.Stat(sub, name); err == nil && !st.IsDir() {
		s.serveAsset(w, r, sub, name)
		return
	}
	fallback := "200.html"
	if _, err := fs.Stat(sub, fallback); err != nil {
		fallback = "index.html"
	}
	w.Header().Set("Cache-Control", "no-cache")
	s.serveAsset(w, r, sub, fallback)
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, sub fs.FS, name string) {
	data, err := fs.ReadFile(sub, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// ---- self route lifecycle ----

func (s *Server) selfRoute(port int) model.Route {
	dial := fmt.Sprintf("127.0.0.1:%d", port)
	return model.Route{
		"@id":      core.SelfID,
		"match":    []any{map[string]any{"host": []any{s.opts.Domain}}},
		"handle":   []any{map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": dial}}}},
		"terminal": true,
	}
}

func (s *Server) self503() model.Route {
	return model.Route{
		"@id":      core.SelfID,
		"match":    []any{map[string]any{"host": []any{s.opts.Domain}}},
		"handle":   []any{map[string]any{"handler": "static_response", "status_code": 503, "body": "caddykit web is stopped\n"}},
		"terminal": true,
	}
}

func (s *Server) readRoutes(ctx context.Context) ([]model.Route, string, bool, error) {
	body, etag, err := s.caddy.GetWithETag(ctx, caddy.ServerPath(s.svc.Server))
	if err != nil {
		return nil, "", false, err
	}
	var obj map[string]any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return nil, "", false, err
	}
	raw, has := obj["routes"]
	routes := []model.Route{}
	if arr, ok := raw.([]any); ok {
		for _, e := range arr {
			if m, ok := e.(map[string]any); ok {
				routes = append(routes, model.Route(m))
			}
		}
	}
	return routes, etag, has, nil
}

func (s *Server) writeRoutes(ctx context.Context, routes []model.Route, etag string, exists bool) error {
	body, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	if exists {
		return s.caddy.Patch(ctx, caddy.RoutesPath(s.svc.Server), body, etag)
	}
	return s.caddy.Put(ctx, caddy.RoutesPath(s.svc.Server), body, etag)
}

func (s *Server) ensureSelfRoute(ctx context.Context, port int) (bool, error) {
	desired := s.selfRoute(port)
	return s.putSelf(ctx, desired)
}

// putSelf writes the self route, refusing to overwrite a foreign route on the
// same domain. Returns true when the route did not exist before.
func (s *Server) putSelf(ctx context.Context, desired model.Route) (bool, error) {
	created := false
	for attempt := 1; attempt <= 3; attempt++ {
		routes, etag, exists, err := s.readRoutes(ctx)
		if err != nil {
			return false, err
		}
		for _, r := range routes {
			if r.ID() == core.SelfID {
				continue
			}
			if model.RoutesCollide(r, desired) {
				return false, fmt.Errorf("domain %q is already used by route %v; refusing to overwrite foreign configuration", s.opts.Domain, r.Hosts())
			}
		}
		idx := -1
		for i := range routes {
			if routes[i].ID() == core.SelfID {
				if idx >= 0 {
					return false, errors.New("multiple caddykit/self routes exist; remove the duplicate first")
				}
				idx = i
			}
		}
		out := make([]model.Route, len(routes))
		copy(out, routes)
		if idx >= 0 {
			out[idx] = desired
		} else {
			created = true
			out = append([]model.Route{desired}, out...)
		}
		if err := s.writeRoutes(ctx, out, etag, exists); err != nil {
			if errors.Is(err, caddy.ErrPreconditionFailed) {
				continue
			}
			return false, err
		}
		return created, nil
	}
	return false, errors.New("could not write caddykit/self after 3 attempts")
}

func (s *Server) removeSelfRoute(ctx context.Context) error {
	for attempt := 1; attempt <= 3; attempt++ {
		routes, etag, exists, err := s.readRoutes(ctx)
		if err != nil {
			return err
		}
		out := make([]model.Route, 0, len(routes))
		found := false
		for _, r := range routes {
			if r.ID() == core.SelfID {
				found = true
				continue
			}
			out = append(out, r)
		}
		if !found {
			return nil
		}
		if err := s.writeRoutes(ctx, out, etag, exists); err != nil {
			if errors.Is(err, caddy.ErrPreconditionFailed) {
				continue
			}
			return err
		}
		return nil
	}
	return errors.New("could not remove caddykit/self")
}

func (s *Server) setSelf503(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.putSelf(ctx, s.self503())
	return err
}
