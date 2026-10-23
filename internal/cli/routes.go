package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"devproxy/internal/config"
	"devproxy/internal/core"
	"devproxy/internal/model"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

type formFlags struct {
	domain     string
	upstream   string
	pathPrefix string
	tlsName    string
	template   string
	params     stringList

	setReq  stringList
	delReq  stringList
	setResp stringList
	delResp stringList

	strip       bool
	upstreamTLS bool
	insecure    bool

	jsonEdit bool
	yes      bool
	jsonOut  bool
}

func (f *formFlags) register(fs *flag.FlagSet, edit bool) {
	fs.StringVar(&f.domain, "domain", "", "route host")
	fs.StringVar(&f.upstream, "upstream", "", "upstream host:port")
	fs.StringVar(&f.pathPrefix, "path-prefix", "", "path prefix (/p)")
	fs.BoolVar(&f.strip, "strip-prefix", false, "strip the path prefix before proxying")
	fs.BoolVar(&f.upstreamTLS, "upstream-tls", false, "use HTTPS to the upstream")
	fs.StringVar(&f.tlsName, "tls-server-name", "", "TLS server name for the upstream")
	fs.BoolVar(&f.insecure, "insecure-skip-verify", false, "disable upstream certificate verification (requires --allow-insecure)")
	fs.Var(&f.setReq, "set-request-header", "K=V (repeatable)")
	fs.Var(&f.delReq, "del-request-header", "K (repeatable)")
	fs.Var(&f.setResp, "set-response-header", "K=V (repeatable)")
	fs.Var(&f.delResp, "del-response-header", "K (repeatable)")
	if edit {
		fs.BoolVar(&f.jsonEdit, "json-edit", false, "edit the route JSON in $EDITOR")
	} else {
		fs.StringVar(&f.template, "template", "", "create from a template")
		fs.Var(&f.params, "param", "template parameter k=v (repeatable)")
	}
	fs.BoolVar(&f.yes, "yes", false, "skip confirmation")
	fs.BoolVar(&f.jsonOut, "json", false, "machine readable output")
}

func (f *formFlags) spec(base *model.Spec, set map[string]bool) (model.Spec, error) {
	var sp model.Spec
	if base != nil {
		sp = *base
	}
	if base == nil || set["domain"] {
		sp.Host = f.domain
	}
	if base == nil || set["upstream"] {
		sp.Upstream = f.upstream
	}
	prefix := strings.TrimSuffix(sp.Path, "*")
	if set["path-prefix"] {
		sp.Path = model.PathPrefixMatcher(f.pathPrefix)
		prefix = strings.TrimSuffix(f.pathPrefix, "*")
	}
	if set["strip-prefix"] && f.strip {
		if prefix == "" {
			return sp, errors.New("--strip-prefix requires --path-prefix")
		}
		sp.StripPrefix = prefix
	}
	if set["upstream-tls"] && f.upstreamTLS {
		sp.UpstreamTLS = true
	}
	if set["tls-server-name"] {
		sp.TLSServerName = f.tlsName
	}
	if set["insecure-skip-verify"] {
		sp.InsecureSkipVerify = f.insecure
	}
	if sp.InsecureSkipVerify && !sp.UpstreamTLS {
		return sp, errors.New("--insecure-skip-verify requires --upstream-tls")
	}
	if len(f.setReq)+len(f.delReq)+len(f.setResp)+len(f.delResp) > 0 {
		if sp.RequestSet == nil {
			sp.RequestSet = map[string][]string{}
		}
		if sp.ResponseSet == nil {
			sp.ResponseSet = map[string][]string{}
		}
	}
	for _, kv := range f.setReq {
		k, v, err := splitKV(kv)
		if err != nil {
			return sp, err
		}
		sp.RequestSet[k] = append(sp.RequestSet[k], v)
	}
	sp.RequestDelete = append(sp.RequestDelete, f.delReq...)
	for _, kv := range f.setResp {
		k, v, err := splitKV(kv)
		if err != nil {
			return sp, err
		}
		sp.ResponseSet[k] = append(sp.ResponseSet[k], v)
	}
	sp.ResponseDelete = append(sp.ResponseDelete, f.delResp...)
	if err := sp.Validate(); err != nil {
		return sp, err
	}
	return sp, nil
}

func splitKV(s string) (string, string, error) {
	i := strings.IndexByte(s, '=')
	if i <= 0 {
		return "", "", fmt.Errorf("expected K=V, got %q", s)
	}
	return s[:i], s[i+1:], nil
}

func parseParams(in []string) (map[string]string, error) {
	out := map[string]string{}
	for _, s := range in {
		k, v, err := splitKV(s)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

func runRoutes(args []string) error {
	if len(args) == 0 {
		return &usageError{errors.New("routes needs a subcommand")}
	}
	sub := args[0]
	rest := args[1:]
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("routes "+sub, flag.ContinueOnError)
	cfg.RegisterCommon(fs)
	var f formFlags
	switch sub {
	case "list":
		var all, jsonOut bool
		fs.BoolVar(&all, "all", false, "include foreign routes")
		fs.BoolVar(&jsonOut, "json", false, "machine readable output")
		pos, err := parseInterspersed(fs, rest)
		if err != nil {
			return wrapParse(err)
		}
		if len(pos) != 0 {
			return &usageError{errors.New("routes list takes no arguments")}
		}
		ctx := context.Background()
		b, err := openBackend(ctx, &cfg)
		if err != nil {
			return err
		}
		defer b.Close()
		views, err := b.List(ctx, all)
		if err != nil {
			return err
		}
		if jsonOut {
			return outputJSON(map[string]any{"routes": views})
		}
		return printRouteList(views)

	case "show":
		var jsonOut bool
		fs.BoolVar(&jsonOut, "json", false, "machine readable output")
		pos, err := parseInterspersed(fs, rest)
		if err != nil {
			return wrapParse(err)
		}
		if len(pos) != 1 {
			return &usageError{errors.New("routes show <route>")}
		}
		ctx := context.Background()
		b, err := openBackend(ctx, &cfg)
		if err != nil {
			return err
		}
		defer b.Close()
		v, err := b.Show(ctx, pos[0])
		if err != nil {
			return err
		}
		if jsonOut {
			return outputJSON(v)
		}
		fmt.Printf("status:   %s\n", statusBadge(*v))
		fmt.Printf("id:       %s\n", v.ID)
		fmt.Printf("host:     %s\n", v.Host)
		fmt.Printf("paths:    %s\n", strings.Join(v.Paths, ","))
		fmt.Printf("upstream: %s\n", v.Upstream)
		if v.Source != "" {
			fmt.Printf("source:   %s\n", v.Source)
		}
		if v.Reason != nil {
			fmt.Printf("json-only: %s (%s)\n", v.Reason.Message, v.Reason.Code)
		}
		if v.Route != nil {
			data, _ := json.MarshalIndent(v.Route, "", "  ")
			fmt.Println(string(data))
		}
		return nil

	case "add":
		f.register(fs, false)
		pos, err := parseInterspersed(fs, rest)
		if err != nil {
			return wrapParse(err)
		}
		if len(pos) != 0 {
			return &usageError{errors.New("routes add takes flags only")}
		}
		ctx := context.Background()
		b, err := openBackend(ctx, &cfg)
		if err != nil {
			return err
		}
		defer b.Close()
		var p *core.Preview
		if f.template != "" {
			params, err := parseParams(f.params)
			if err != nil {
				return &usageError{err}
			}
			var reqs []string
			p, reqs, err = b.PreviewCreateTemplate(ctx, f.template, params)
			if err != nil {
				return err
			}
			if !f.jsonOut {
				for _, r := range reqs {
					fmt.Println("requirement:", r)
				}
			}
		} else {
			set := visited(fs)
			sp, err := f.spec(nil, set)
			if err != nil {
				return &usageError{err}
			}
			if sp.Host == "" || sp.Upstream == "" {
				return &usageError{errors.New("routes add requires --domain and --upstream (or --template)")}
			}
			p, err = b.PreviewCreate(ctx, sp)
			if err != nil {
				return err
			}
		}
		return previewAndApply(ctx, b, p, f.yes, f.jsonOut)

	case "edit":
		f.register(fs, true)
		pos, err := parseInterspersed(fs, rest)
		if err != nil {
			return wrapParse(err)
		}
		if len(pos) != 1 {
			return &usageError{errors.New("routes edit <route> [--json-edit | flags]")}
		}
		ctx := context.Background()
		b, err := openBackend(ctx, &cfg)
		if err != nil {
			return err
		}
		defer b.Close()
		v, err := b.Show(ctx, pos[0])
		if err != nil {
			return err
		}
		if v.Route == nil {
			return &core.BlockedError{Reason: model.Reason{Code: core.BlockRouteMissing, Message: "route is missing in Caddy; resolve the conflict first"}}
		}
		if v.Self {
			return core.ErrSelfProtected
		}
		var p *core.Preview
		if f.jsonEdit {
			raw, err := editRouteFile(v.Route, v.ID)
			if err != nil {
				return err
			}
			p, err = b.PreviewEditJSON(ctx, pos[0], raw)
			if err != nil {
				return err
			}
		} else {
			cur, class, reason := model.Extract(v.Route)
			if class != model.FormEditable || reason != nil {
				if reason == nil {
					reason = &model.Reason{Code: core.BlockJSONOnly, Message: "route is not form-editable"}
				}
				return &core.BlockedError{Reason: *reason}
			}
			set := visited(fs)
			sp, err := f.spec(&cur, set)
			if err != nil {
				return &usageError{err}
			}
			p, err = b.PreviewEditForm(ctx, pos[0], sp)
			if err != nil {
				return err
			}
		}
		return previewAndApply(ctx, b, p, f.yes, f.jsonOut)

	case "adopt", "unmanage", "delete":
		f.register(fs, false)
		pos, err := parseInterspersed(fs, rest)
		if err != nil {
			return wrapParse(err)
		}
		if len(pos) != 1 {
			return &usageError{fmt.Errorf("routes %s <route>", sub)}
		}
		ctx := context.Background()
		b, err := openBackend(ctx, &cfg)
		if err != nil {
			return err
		}
		defer b.Close()
		var p *core.Preview
		switch sub {
		case "adopt":
			p, err = b.PreviewAdopt(ctx, pos[0])
		case "unmanage":
			p, err = b.PreviewUnmanage(ctx, pos[0])
		case "delete":
			p, err = b.PreviewDelete(ctx, pos[0])
		}
		if err != nil {
			return err
		}
		return previewAndApply(ctx, b, p, f.yes, f.jsonOut)

	default:
		return &usageError{fmt.Errorf("unknown routes command %q", sub)}
	}
}

func openBackend(ctx context.Context, cfg *config.Config) (*Backend, error) {
	if err := cfg.Validate(false); err != nil {
		return nil, &usageError{err}
	}
	return OpenBackend(ctx, cfg)
}

func visited(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

func editRouteFile(raw model.Route, expectedID string) (model.Route, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return nil, errors.New("$EDITOR is empty")
	}
	f, err := os.CreateTemp("", "devproxy-route-*.json")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	data, _ := json.MarshalIndent(raw, "", "  ")
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	for {
		cmd := exec.Command(parts[0], append(parts[1:], name)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		route, err := model.Decode(data)
		if err == nil {
			err = model.ValidateJSONRoute(route, expectedID)
		}
		if err == nil {
			return route, nil
		}
		fmt.Fprintln(os.Stderr, "invalid route:", err)
		if !isTTY(os.Stdin) {
			return nil, fmt.Errorf("%w: %v", core.ErrInvalid, err)
		}
		fmt.Fprint(os.Stderr, "Press Enter to edit again, Ctrl+C to abort... ")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
}
