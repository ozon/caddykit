package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"golang.org/x/sys/unix"

	"devproxy/internal/config"
	"devproxy/internal/core"
	"devproxy/internal/templates"
	"devproxy/internal/web"
)

var errNeedConfirmation = errors.New("confirmation required")

type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `devproxy — manage Caddy proxy routes

Usage:
  devproxy web --domain <domain> [options]
  devproxy routes list [--json] [--all]
  devproxy routes show <route> [--json]
  devproxy routes add --domain <d> --upstream <host:port> [options]
  devproxy routes add --template <name> --param k=v ...
  devproxy routes edit <route> [--json-edit | form flags]
  devproxy routes adopt <route|host> [--yes]
  devproxy routes unmanage <route> [--yes]
  devproxy routes delete <route> [--yes]
  devproxy conflicts list [--json]
  devproxy conflicts resolve <route> --action take-current|apply-desired|unmanage [--yes]
  devproxy undo [--yes] [--json]
  devproxy templates list [--json]
  devproxy templates show <name> [--json]

Common options: --caddy-admin, --data-dir, --template-dir, --server
                --allow-remote-admin, --allow-insecure
`)
}

func Run(args []string, version string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	var err error
	switch args[0] {
	case "web":
		err = runWeb(args[1:], version)
	case "routes":
		err = runRoutes(args[1:])
	case "conflicts":
		err = runConflicts(args[1:])
	case "undo":
		err = runUndo(args[1:])
	case "templates":
		err = runTemplates(args[1:])
	case "version":
		fmt.Println(version)
		return 0
	case "help", "-h", "--help":
		usage(os.Stdout)
		return 0
	default:
		err = &usageError{fmt.Errorf("unknown command %q", args[0])}
	}
	if err == nil {
		return 0
	}
	if errors.Is(err, errNeedConfirmation) {
		fmt.Fprintln(os.Stderr, "devproxy: confirmation required (use --yes)")
		return 3
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintln(os.Stderr, "devproxy:", err)
	return codeFor(err)
}

func codeFor(err error) int {
	switch {
	case errors.Is(err, ErrInstance):
		return 6
	case errors.Is(err, ErrAuth):
		return 5
	case errors.Is(err, ErrCaddyUnreach):
		return 4
	case errors.Is(err, core.ErrBlocked),
		errors.Is(err, core.ErrStale),
		errors.Is(err, core.ErrUndoBlocked),
		errors.Is(err, core.ErrNoUndo),
		errors.Is(err, core.ErrJournalOpen),
		errors.Is(err, core.ErrAmbiguous),
		errors.Is(err, core.ErrSelfProtected):
		return 3
	case errors.Is(err, core.ErrInvalid), errors.Is(err, core.ErrNotFound):
		return 2
	default:
		var ue *usageError
		if errors.As(err, &ue) {
			return 2
		}
		return 1
	}
}

func runWeb(args []string, version string) error {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	cfg.Register(fs)
	if _, err := parseInterspersed(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return &usageError{err}
	}
	if err := cfg.Validate(true); err != nil {
		return &usageError{err}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := web.Run(ctx, web.Options{
		CaddyAdmin:        cfg.CaddyAdmin,
		Domain:            cfg.Domain,
		Server:            cfg.Server,
		DataDir:           cfg.DataDir,
		TemplateDir:       cfg.TemplateDir,
		ReconcileInterval: cfg.ReconcileInterval,
		AllowInsecure:     cfg.AllowInsecure,
		Version:           version,
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, web.ErrInstance):
		return ErrInstance
	case core.IsCaddyUnreachable(err):
		return fmt.Errorf("%w: %v", ErrCaddyUnreach, err)
	default:
		return err
	}
}

func runConflicts(args []string) error {
	if len(args) == 0 {
		return &usageError{errors.New("conflicts needs list or resolve")}
	}
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("conflicts", flag.ContinueOnError)
	cfg.RegisterCommon(fs)
	var jsonOut, yes bool
	switch args[0] {
	case "list":
		fs.BoolVar(&jsonOut, "json", false, "machine readable output")
		pos, err := parseInterspersed(fs, args[1:])
		if err != nil {
			return wrapParse(err)
		}
		if len(pos) != 0 {
			return &usageError{errors.New("conflicts list takes no arguments")}
		}
		ctx := context.Background()
		b, err := OpenBackend(ctx, &cfg)
		if err != nil {
			return err
		}
		defer b.Close()
		cs, err := b.Conflicts(ctx)
		if err != nil {
			return err
		}
		if jsonOut {
			return outputJSON(map[string]any{"conflicts": cs})
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "KIND\tHOST\tROUTE\tREASON")
		for _, c := range cs {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.Kind, c.Host, c.RouteID, c.Reason)
		}
		return w.Flush()
	case "resolve":
		var action string
		fs.StringVar(&action, "action", "", "take-current|apply-desired|unmanage")
		fs.BoolVar(&yes, "yes", false, "skip confirmation")
		fs.BoolVar(&jsonOut, "json", false, "machine readable output")
		pos, err := parseInterspersed(fs, args[1:])
		if err != nil {
			return wrapParse(err)
		}
		if len(pos) != 1 || action == "" {
			return &usageError{errors.New("conflicts resolve <route> --action <action>")}
		}
		ctx := context.Background()
		b, err := OpenBackend(ctx, &cfg)
		if err != nil {
			return err
		}
		defer b.Close()
		p, err := b.PreviewResolve(ctx, pos[0], action)
		if err != nil {
			return err
		}
		return previewAndApply(ctx, b, p, yes, jsonOut)
	default:
		return &usageError{fmt.Errorf("unknown conflicts command %q", args[0])}
	}
}

func runUndo(args []string) error {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("undo", flag.ContinueOnError)
	cfg.RegisterCommon(fs)
	var yes, jsonOut bool
	fs.BoolVar(&yes, "yes", false, "skip confirmation")
	fs.BoolVar(&jsonOut, "json", false, "machine readable output")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return wrapParse(err)
	}
	if len(pos) != 0 {
		return &usageError{errors.New("undo takes no arguments")}
	}
	ctx := context.Background()
	b, err := OpenBackend(ctx, &cfg)
	if err != nil {
		return err
	}
	defer b.Close()
	u, err := b.UndoInfo(ctx)
	if err != nil {
		return err
	}
	if u == nil {
		if jsonOut {
			return outputJSON(map[string]any{"available": false})
		}
		fmt.Println("Nothing to undo.")
		return nil
	}
	if jsonOut && !yes {
		_ = outputJSON(map[string]any{"available": true, "kind": u.Kind, "description": u.Description, "created_at": u.CreatedAt, "applied": false})
		return errNeedConfirmation
	}
	if !jsonOut || !yes {
		if !yes {
			fmt.Printf("Undo: %s\n", u.Description)
		}
	}
	if !yes {
		ok, err := confirm("Undo this step?")
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("aborted")
		}
	}
	res, err := b.Undo(ctx)
	if err != nil {
		return err
	}
	if jsonOut {
		return outputJSON(res)
	}
	fmt.Printf("Undone: %s\n", res.Message)
	return nil
}

func runTemplates(args []string) error {
	if len(args) == 0 {
		return &usageError{errors.New("templates needs list or show")}
	}
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("templates", flag.ContinueOnError)
	cfg.RegisterCommon(fs)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "machine readable output")
	pos, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return wrapParse(err)
	}
	if err := cfg.Validate(false); err != nil {
		return &usageError{err}
	}
	list, err := templates.Load(cfg.TemplateDir)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		if len(pos) != 0 {
			return &usageError{errors.New("templates list takes no arguments")}
		}
		if jsonOut {
			return outputJSON(map[string]any{"templates": list})
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tNAME\tDESCRIPTION")
		for _, t := range list {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", t.ID, t.Name, t.Description)
		}
		return w.Flush()
	case "show":
		if len(pos) != 1 {
			return &usageError{errors.New("templates show <name>")}
		}
		var t *templates.Template
		for i := range list {
			if list[i].ID == pos[0] {
				t = &list[i]
				break
			}
		}
		if t == nil {
			return fmt.Errorf("%w: template %q", core.ErrNotFound, pos[0])
		}
		if jsonOut {
			return outputJSON(t)
		}
		fmt.Printf("%s (%s)\n%s\n\nParameters:\n", t.Name, t.ID, t.Description)
		for _, p := range t.Parameters {
			req := "optional"
			if p.Required {
				req = "required"
			}
			fmt.Printf("  %-16s %-12s %s\n", p.Name, p.Type, req)
			if p.Description != "" {
				fmt.Printf("  %-16s %s\n", "", p.Description)
			}
		}
		if len(t.Requirements) > 0 {
			fmt.Println("\nRequirements:")
			for _, r := range t.Requirements {
				fmt.Println("  -", r)
			}
		}
		return nil
	default:
		return &usageError{fmt.Errorf("unknown templates command %q", args[0])}
	}
}

// ---- shared helpers ----

func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				flags = append(flags, a)
				continue
			}
			f := fs.Lookup(name)
			if f == nil {
				flags = append(flags, a)
				continue
			}
			flags = append(flags, a)
			if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
				continue
			}
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		pos = append(pos, a)
	}
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	return pos, nil
}

func wrapParse(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return err
	}
	return &usageError{err}
}

func isTTY(f *os.File) bool {
	// A stat mode check is not enough: /dev/null is also a character device.
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}

func confirm(prompt string) (bool, error) {
	if !isTTY(os.Stdin) {
		return false, errNeedConfirmation
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes", nil
}

func outputJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printPreview(p *core.Preview) {
	fmt.Println(p.Description)
	for _, c := range p.Diff {
		switch c.Op {
		case "add":
			fmt.Printf("+ %s %s\n", c.Path, compactJSON(c.After))
		case "remove":
			fmt.Printf("- %s %s\n", c.Path, compactJSON(c.Before))
		case "replace":
			fmt.Printf("~ %s: %s -> %s\n", c.Path, compactJSON(c.Before), compactJSON(c.After))
		}
	}
	if p.Blocked != nil {
		fmt.Printf("BLOCKED (%s): %s\n", p.Blocked.Code, p.Blocked.Message)
	}
	for _, w := range p.Warnings {
		fmt.Println("warning:", w)
	}
}

func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	s := string(b)
	if len(s) > 300 {
		s = s[:297] + "..."
	}
	return s
}

func previewAndApply(ctx context.Context, b *Backend, p *core.Preview, yes, jsonOut bool) error {
	if p.Blocked != nil {
		if jsonOut {
			_ = outputJSON(map[string]any{"preview": p, "applied": false})
		} else {
			printPreview(p)
		}
		return &core.BlockedError{Reason: *p.Blocked}
	}
	if p.Kind == core.KindEdit && len(p.Diff) == 0 {
		if jsonOut {
			return outputJSON(map[string]any{"preview": p, "applied": false, "message": "no changes"})
		}
		fmt.Println("No changes.")
		return nil
	}
	if jsonOut && !yes {
		_ = outputJSON(map[string]any{"preview": p, "applied": false})
		return errNeedConfirmation
	}
	if !jsonOut {
		printPreview(p)
		if !yes {
			ok, err := confirm("Apply these changes?")
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("aborted")
			}
		}
	}
	res, err := b.Apply(ctx, p)
	if err != nil {
		return err
	}
	if jsonOut {
		return outputJSON(res)
	}
	fmt.Println("OK:", res.Message)
	return nil
}

func statusBadge(v core.RouteView) string {
	if v.Self {
		return "self"
	}
	switch v.Status {
	case "managed_sync":
		return "synced"
	case "externally_modified":
		return "modified"
	case "missing_restoreable":
		return "missing"
	case "foreign":
		return "foreign"
	default:
		return v.Status
	}
}

func printRouteList(views []core.RouteView) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "STATUS\tHOST\tPATH\tUPSTREAM\tSOURCE\tID")
	for _, v := range views {
		path := strings.Join(v.Paths, ",")
		if path == "" {
			path = "*"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", statusBadge(v), v.Host, path, v.Upstream, v.Source, v.ID)
	}
	return w.Flush()
}
