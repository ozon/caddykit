package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"time"

	"caddykit/internal/caddy"
	"caddykit/internal/config"
	"caddykit/internal/core"
	"caddykit/internal/ipc"
	"caddykit/internal/model"
	"caddykit/internal/state"
	"caddykit/internal/templates"
)

var (
	ErrInstance     = errors.New("another caddykit instance is running")
	ErrAuth         = errors.New("authentication or IPC failed")
	ErrCaddyUnreach = errors.New("caddy admin API is unreachable")
)

// Backend forwards to the running web process when possible, otherwise runs a
// single one-shot operation under the instance and state locks.
type Backend struct {
	remote   *ipc.Client
	svc      *core.Service
	instance *state.FileLock
	store    *state.Store
	cfg      *config.Config
}

func OpenBackend(ctx context.Context, cfg *config.Config) (*Backend, error) {
	if c, err := ipc.Dial(cfg.DataDir); err == nil {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ok := c.Ping(pctx)
		cancel()
		if ok {
			return &Backend{remote: c, cfg: cfg}, nil
		}
	}
	if err := config.EnsureDataDir(cfg.DataDir); err != nil {
		return nil, err
	}
	store, err := state.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	inst, err := state.TryLock(store.Path(state.FileInstance))
	if err != nil {
		if errors.Is(err, state.ErrLocked) {
			return nil, ErrInstance
		}
		return nil, err
	}
	release := func() {
		store.ReleaseStateLock()
		_ = inst.Close()
	}
	if err := store.HoldStateLock(); err != nil {
		_ = inst.Close()
		return nil, fmt.Errorf("%w: %v", ErrAuth, err)
	}
	cc, err := caddy.New(cfg.CaddyAdmin)
	if err != nil {
		release()
		return nil, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := cc.Check(checkCtx); err != nil {
		release()
		return nil, fmt.Errorf("%w: %v", ErrCaddyUnreach, err)
	}
	svc := core.New(cc, store, cfg.Server)
	svc.AllowInsecure = cfg.AllowInsecure
	if svc.Server == "" {
		if st, err := store.LoadState(); err == nil && st.Server != "" {
			svc.Server = st.Server
		}
	}
	if err := svc.ValidateServer(checkCtx); err != nil {
		release()
		return nil, err
	}
	if err := svc.Recover(checkCtx); err != nil {
		// An unclear operation is not fatal for read-only commands, but the
		// error is meaningful for mutations. Keep the backend usable.
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	return &Backend{svc: svc, instance: inst, store: store, cfg: cfg}, nil
}

func (b *Backend) Close() {
	if b == nil {
		return
	}
	if b.store != nil {
		b.store.ReleaseStateLock()
	}
	if b.instance != nil {
		_ = b.instance.Close()
	}
}

func (b *Backend) Remote() bool { return b.remote != nil }

func mapAPIError(err error) error {
	var ae *ipc.APIError
	if errors.As(err, &ae) {
		switch ae.Code {
		case "PREVIEW_STALE":
			return fmt.Errorf("%w: %s", core.ErrStale, ae.Message)
		case "JOURNAL_OPEN":
			return fmt.Errorf("%w: %s", core.ErrJournalOpen, ae.Message)
		case "UNDO_BLOCKED":
			return fmt.Errorf("%w: %s", core.ErrUndoBlocked, ae.Message)
		case "NO_UNDO":
			return fmt.Errorf("%w: %s", core.ErrNoUndo, ae.Message)
		case "SELF_PROTECTED":
			return core.ErrSelfProtected
		case "BLOCKED":
			return &core.BlockedError{Reason: model.Reason{Code: ae.BlockReason, Message: ae.Message}}
		case "AMBIGUOUS":
			return &core.AmbiguousError{Selector: "", Candidates: ae.Candidates}
		case "INVALID":
			return fmt.Errorf("%w: %s", core.ErrInvalid, ae.Message)
		case "NOT_FOUND":
			return fmt.Errorf("%w: %s", core.ErrNotFound, ae.Message)
		case "UNAUTHORIZED", "CSRF":
			return fmt.Errorf("%w: %s", ErrAuth, ae.Message)
		case "CADDY_UNREACHABLE":
			return fmt.Errorf("%w: %s", ErrCaddyUnreach, ae.Message)
		}
		if ae.Status == 502 {
			return fmt.Errorf("%w: %s", ErrCaddyUnreach, ae.Message)
		}
		return err
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%w: %v", ErrCaddyUnreach, err)
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return fmt.Errorf("%w: %v", ErrCaddyUnreach, err)
	}
	return err
}

func (b *Backend) List(ctx context.Context, all bool) ([]core.RouteView, error) {
	if b.remote != nil {
		var out struct {
			Routes []core.RouteView `json:"routes"`
		}
		q := "/api/v1/routes"
		if !all {
			q += "?all=0"
		}
		return out.Routes, mapAPIError(b.remote.Do(ctx, "GET", q, nil, &out))
	}
	return b.svc.List(ctx, all)
}

func (b *Backend) Show(ctx context.Context, id string) (*core.RouteView, error) {
	if b.remote != nil {
		var out core.RouteView
		if err := b.remote.Do(ctx, "GET", "/api/v1/routes/"+url.PathEscape(id), nil, &out); err != nil {
			return nil, mapAPIError(err)
		}
		return &out, nil
	}
	return b.svc.Show(ctx, id)
}

type previewRequest struct {
	Kind     string            `json:"kind"`
	ID       string            `json:"id,omitempty"`
	Spec     *model.Spec       `json:"spec,omitempty"`
	Route    json.RawMessage   `json:"route,omitempty"`
	Template string            `json:"template,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
}

type previewEnvelope struct {
	*core.Preview
	Requirements []string `json:"requirements,omitempty"`
}

func (b *Backend) preview(ctx context.Context, req previewRequest, local func() (*core.Preview, error)) (*core.Preview, error) {
	if b.remote != nil {
		var out previewEnvelope
		if err := b.remote.Do(ctx, "POST", "/api/v1/routes/preview", req, &out); err != nil {
			return nil, mapAPIError(err)
		}
		return out.Preview, nil
	}
	return local()
}

func (b *Backend) PreviewCreate(ctx context.Context, sp model.Spec) (*core.Preview, error) {
	return b.preview(ctx, previewRequest{Kind: "create", Spec: &sp}, func() (*core.Preview, error) {
		return b.svc.PreviewCreate(ctx, sp, "create", nil)
	})
}

func (b *Backend) PreviewCreateTemplate(ctx context.Context, name string, params map[string]string) (*core.Preview, []string, error) {
	if b.remote != nil {
		var out previewEnvelope
		err := b.remote.Do(ctx, "POST", "/api/v1/routes/preview", previewRequest{Kind: "create", Template: name, Params: params}, &out)
		if err != nil {
			return nil, nil, mapAPIError(err)
		}
		return out.Preview, out.Requirements, nil
	}
	tmpl, err := templates.Get(b.cfg.TemplateDir, name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", core.ErrInvalid, err)
	}
	sp, reqs, err := tmpl.Render(params)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", core.ErrInvalid, err)
	}
	p, err := b.svc.PreviewCreate(ctx, sp, "template", &state.TemplateRef{Name: tmpl.ID, Version: "1"})
	if err != nil {
		return nil, nil, err
	}
	sensitive := tmpl.SensitiveValues(params)
	p.Diff = model.RedactValues(p.Diff, sensitive)
	p.Description = model.RedactText(p.Description, sensitive)
	for i := range p.Warnings {
		p.Warnings[i] = model.RedactText(p.Warnings[i], sensitive)
	}
	for i := range reqs {
		reqs[i] = model.RedactText(reqs[i], sensitive)
	}
	return p, reqs, nil
}

func (b *Backend) PreviewEditForm(ctx context.Context, id string, sp model.Spec) (*core.Preview, error) {
	return b.preview(ctx, previewRequest{Kind: "edit", ID: id, Spec: &sp}, func() (*core.Preview, error) {
		return b.svc.PreviewEditForm(ctx, id, sp)
	})
}

func (b *Backend) PreviewEditJSON(ctx context.Context, id string, raw model.Route) (*core.Preview, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	return b.preview(ctx, previewRequest{Kind: "edit", ID: id, Route: data}, func() (*core.Preview, error) {
		return b.svc.PreviewEditJSON(ctx, id, raw)
	})
}

func (b *Backend) PreviewDelete(ctx context.Context, id string) (*core.Preview, error) {
	return b.preview(ctx, previewRequest{Kind: "delete", ID: id}, func() (*core.Preview, error) {
		return b.svc.PreviewDelete(ctx, id)
	})
}

func (b *Backend) PreviewAdopt(ctx context.Context, id string) (*core.Preview, error) {
	return b.preview(ctx, previewRequest{Kind: "adopt", ID: id}, func() (*core.Preview, error) {
		return b.svc.PreviewAdopt(ctx, id)
	})
}

func (b *Backend) PreviewUnmanage(ctx context.Context, id string) (*core.Preview, error) {
	return b.preview(ctx, previewRequest{Kind: "unmanage", ID: id}, func() (*core.Preview, error) {
		return b.svc.PreviewUnmanage(ctx, id)
	})
}

func (b *Backend) PreviewResolve(ctx context.Context, id, action string) (*core.Preview, error) {
	// action is take-current|apply-desired|unmanage; the resolve endpoint is
	// authoritative for conflict actions.
	if b.remote != nil {
		var out previewEnvelope
		err := b.remote.Do(ctx, "POST", "/api/v1/conflicts/resolve", map[string]string{"id": id, "action": action}, &out)
		if err != nil {
			return nil, mapAPIError(err)
		}
		return out.Preview, nil
	}
	return b.svc.PreviewResolve(ctx, id, action)
}

func (b *Backend) Apply(ctx context.Context, p *core.Preview) (*core.Result, error) {
	if b.remote != nil {
		var out core.Result
		if err := b.remote.Do(ctx, "POST", "/api/v1/routes/apply", p, &out); err != nil {
			return nil, mapAPIError(err)
		}
		return &out, nil
	}
	return b.svc.Apply(ctx, p)
}

func (b *Backend) Conflicts(ctx context.Context) ([]core.Conflict, error) {
	if b.remote != nil {
		var out struct {
			Conflicts []core.Conflict `json:"conflicts"`
		}
		return out.Conflicts, mapAPIError(b.remote.Do(ctx, "GET", "/api/v1/conflicts", nil, &out))
	}
	return b.svc.Conflicts(ctx)
}

func (b *Backend) UndoInfo(ctx context.Context) (*state.Undo, error) {
	if b.remote != nil {
		var out struct {
			Available   bool      `json:"available"`
			Kind        string    `json:"kind"`
			Description string    `json:"description"`
			CreatedAt   time.Time `json:"created_at"`
		}
		if err := b.remote.Do(ctx, "GET", "/api/v1/undo", nil, &out); err != nil {
			return nil, mapAPIError(err)
		}
		if !out.Available {
			return nil, nil
		}
		return &state.Undo{Kind: out.Kind, Description: out.Description, CreatedAt: out.CreatedAt}, nil
	}
	return b.svc.UndoInfo()
}

func (b *Backend) Undo(ctx context.Context) (*core.Result, error) {
	if b.remote != nil {
		var out core.Result
		if err := b.remote.Do(ctx, "POST", "/api/v1/undo", map[string]any{}, &out); err != nil {
			return nil, mapAPIError(err)
		}
		return &out, nil
	}
	return b.svc.Undo(ctx)
}

func (b *Backend) Templates() ([]templates.Template, error) {
	return templates.Load(b.cfg.TemplateDir)
}

func (b *Backend) Template(name string) (*templates.Template, error) {
	return templates.Get(b.cfg.TemplateDir, name)
}
