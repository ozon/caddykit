// Package render produces a read-only, best-effort Caddyfile preview from
// Caddy JSON routes. It is never used to write configuration. Caddy itself
// cannot export JSON to a Caddyfile, so unsupported constructs become comments
// and warnings.
package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"caddykit/internal/model"
)

const maxDepth = 32

type renderer struct {
	warnings []string
	matcherN int
}

// Route renders one top-level route as a Caddyfile site block.
func Route(r model.Route) (string, []string) {
	rw := &renderer{}
	var b strings.Builder
	b.WriteString("# Caddyfile preview — read-only approximation.\n")
	b.WriteString("# Caddy cannot export JSON to Caddyfile; unsupported fields are comments.\n\n")
	rw.site(&b, r)
	return b.String(), dedupe(rw.warnings)
}

func (rw *renderer) warn(format string, args ...any) {
	rw.warnings = append(rw.warnings, fmt.Sprintf(format, args...))
}

func (rw *renderer) site(b *strings.Builder, r model.Route) {
	hosts := r.Hosts()
	clean := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h != "" {
			clean = append(clean, h)
		}
	}
	if len(clean) == 0 || (len(clean) == 1 && clean[0] == "*") {
		clean = []string{":443"}
		rw.warn("route has no host matcher; rendered as a :443 site")
	}
	b.WriteString(strings.Join(clean, ", "))
	b.WriteString(" {\n")
	rw.routeBody(b, r, 1, 0, true)
	b.WriteString("}\n")
}

func (rw *renderer) routeBody(b *strings.Builder, r model.Route, indent, depth int, top bool) {
	if depth > maxDepth {
		rw.warn("route nesting exceeds %d levels; preview truncated", maxDepth)
		b.WriteString(indentS(indent) + "# nesting too deep\n")
		return
	}
	if top && !r.Terminal() {
		rw.warn("top-level route has terminal=false; Caddyfile site blocks are terminal")
		b.WriteString(indentS(indent) + "# terminal=false: Caddyfile cannot express fallthrough exactly\n")
	}
	if m, ok := rw.matcher(r, top); ok {
		name := rw.nextMatcher()
		b.WriteString(indentS(indent) + name + " {\n")
		b.WriteString(indentLines(m, indent+1))
		b.WriteString("\n" + indentS(indent) + "}\n")
		b.WriteString(indentS(indent) + "route " + name + " {\n")
		rw.handlers(b, r, indent+1, depth+1)
		b.WriteString(indentS(indent) + "}\n")
		return
	}
	rw.handlers(b, r, indent, depth+1)
}

func (rw *renderer) handlers(b *strings.Builder, r model.Route, indent, depth int) {
	arr, _ := r["handle"].([]any)
	if len(arr) == 0 {
		rw.warn("route has no handlers")
		return
	}
	if len(arr) == 1 {
		if hm, ok := arr[0].(map[string]any); ok && hm["handler"] == "subroute" {
			rw.subroute(b, hm, indent, depth)
			return
		}
	}
	if len(arr) > 1 {
		b.WriteString(indentS(indent) + "route {\n")
		for _, e := range arr {
			hm, ok := e.(map[string]any)
			if !ok {
				rw.warn("handler is not an object")
				continue
			}
			rw.handler(b, hm, indent+1, depth)
		}
		b.WriteString(indentS(indent) + "}\n")
		return
	}
	hm, ok := arr[0].(map[string]any)
	if !ok {
		rw.warn("handler is not an object")
		return
	}
	rw.handler(b, hm, indent, depth)
}

func (rw *renderer) subroute(b *strings.Builder, h map[string]any, indent, depth int) {
	routes, ok := h["routes"].([]any)
	if !ok || len(routes) == 0 {
		rw.warn("subroute has no routes")
		b.WriteString(indentS(indent) + "# empty subroute\n")
		return
	}
	for i, e := range routes {
		rm, ok := e.(map[string]any)
		if !ok {
			rw.warn("subroute.routes[%d] is not an object", i)
			continue
		}
		rw.routeBody(b, model.Route(rm), indent, depth+1, false)
	}
	for k := range h {
		if k != "handler" && k != "routes" {
			rw.warn("subroute field %q is not represented", k)
		}
	}
}

func (rw *renderer) handler(b *strings.Builder, h map[string]any, indent, depth int) {
	name, _ := h["handler"].(string)
	switch name {
	case "reverse_proxy":
		rw.reverseProxy(b, h, indent)
	case "headers":
		rw.headers(b, h, indent)
	case "rewrite":
		rw.rewrite(b, h, indent)
	case "encode":
		rw.encode(b, h, indent)
	case "subroute":
		b.WriteString(indentS(indent) + "route {\n")
		rw.subroute(b, h, indent+1, depth+1)
		b.WriteString(indentS(indent) + "}\n")
	case "static_response":
		rw.staticResponse(b, h, indent)
	case "file_server":
		b.WriteString(indentS(indent) + "file_server\n")
		for k := range h {
			if k != "handler" {
				rw.warn("file_server field %q is not represented", k)
			}
		}
	default:
		b.WriteString(indentS(indent) + "# unsupported handler " + strconv.Quote(name) + ": " + compactJSON(h) + "\n")
		rw.warn("unsupported handler %q", name)
	}
}

func (rw *renderer) reverseProxy(b *strings.Builder, h map[string]any, indent int) {
	ups, _ := h["upstreams"].([]any)
	var dials []string
	for i, e := range ups {
		um, ok := e.(map[string]any)
		if !ok {
			rw.warn("reverse_proxy upstream %d is not an object", i)
			continue
		}
		d, _ := um["dial"].(string)
		if d == "" {
			rw.warn("reverse_proxy upstream %d has no dial", i)
			continue
		}
		dials = append(dials, token(d))
		for k := range um {
			if k != "dial" {
				rw.warn("reverse_proxy upstream field %q is not represented", k)
			}
		}
	}
	if len(dials) == 0 {
		b.WriteString(indentS(indent) + "# reverse_proxy without upstreams\n")
		rw.warn("reverse_proxy has no upstreams")
		return
	}
	b.WriteString(indentS(indent) + "reverse_proxy " + strings.Join(dials, " "))

	var opts []string
	if v, ok := h["flush_interval"]; ok {
		opts = append(opts, "flush_interval "+scalar(v))
	}
	if tr, ok := h["transport"].(map[string]any); ok {
		if p, _ := tr["protocol"].(string); p != "" && p != "http" {
			rw.warn("transport protocol %q is not represented", p)
		}
		if tls, ok := tr["tls"].(map[string]any); ok {
			tlsLines := []string{"tls"}
			if sn, _ := tls["server_name"].(string); sn != "" {
				tlsLines = append(tlsLines, "tls_server_name "+token(sn))
			}
			if isk, _ := tls["insecure_skip_verify"].(bool); isk {
				tlsLines = append(tlsLines, "tls_insecure_skip_verify")
			}
			for k := range tls {
				if k != "server_name" && k != "insecure_skip_verify" {
					rw.warn("TLS field %q is not represented", k)
				}
			}
			opts = append(opts, "transport http {")
			for _, l := range tlsLines {
				opts = append(opts, indentS(1)+l)
			}
			opts = append(opts, "}")
		} else {
			for k := range tr {
				if k != "protocol" {
					rw.warn("transport field %q is not represented", k)
				}
			}
		}
	}
	for k := range h {
		switch k {
		case "handler", "upstreams", "transport", "flush_interval":
		default:
			rw.warn("reverse_proxy field %q is not represented", k)
		}
	}
	if len(opts) == 0 {
		b.WriteString("\n")
		return
	}
	b.WriteString(" {\n")
	for _, o := range opts {
		b.WriteString(indentS(indent+1) + o + "\n")
	}
	b.WriteString(indentS(indent) + "}\n")
}

func (rw *renderer) headers(b *strings.Builder, h map[string]any, indent int) {
	if req, ok := h["request"].(map[string]any); ok {
		for _, op := range headerOps(req) {
			b.WriteString(indentS(indent) + "request_header " + op + "\n")
		}
		for k := range req {
			if k != "set" && k != "add" && k != "delete" {
				rw.warn("headers.request field %q is not represented", k)
			}
		}
	}
	if resp, ok := h["response"].(map[string]any); ok {
		deferred, _ := resp["deferred"].(bool)
		ops := headerOps(resp)
		_, hasDelete := resp["delete"]
		if deferred || hasDelete {
			b.WriteString(indentS(indent) + "header {\n")
			if deferred {
				b.WriteString(indentS(indent+1) + "defer\n")
			}
			for _, op := range ops {
				b.WriteString(indentS(indent+1) + op + "\n")
			}
			b.WriteString(indentS(indent) + "}\n")
		} else {
			for _, op := range ops {
				b.WriteString(indentS(indent) + "header " + op + "\n")
			}
		}
		for k := range resp {
			if k != "set" && k != "add" && k != "delete" && k != "deferred" {
				rw.warn("headers.response field %q is not represented", k)
			}
		}
	}
	for k := range h {
		if k != "handler" && k != "request" && k != "response" {
			rw.warn("headers field %q is not represented", k)
		}
	}
}

func headerOps(m map[string]any) []string {
	var ops []string
	if set, ok := m["set"].(map[string]any); ok {
		for _, name := range sortedKeys(set) {
			vals := stringValues(set[name])
			for i, v := range vals {
				prefix := ""
				if i > 0 {
					prefix = "+"
				}
				ops = append(ops, prefix+token(name)+" "+token(v))
			}
		}
	}
	if add, ok := m["add"].(map[string]any); ok {
		for _, name := range sortedKeys(add) {
			for _, v := range stringValues(add[name]) {
				ops = append(ops, "+"+token(name)+" "+token(v))
			}
		}
	}
	if del := stringValues(m["delete"]); len(del) > 0 {
		for _, name := range del {
			ops = append(ops, "-"+token(name))
		}
	}
	return ops
}

func (rw *renderer) rewrite(b *strings.Builder, h map[string]any, indent int) {
	if sp, ok := h["strip_path_prefix"].(string); ok && sp != "" {
		b.WriteString(indentS(indent) + "uri strip_prefix " + token(sp) + "\n")
	}
	for k := range h {
		if k != "handler" && k != "strip_path_prefix" {
			rw.warn("rewrite field %q is not represented", k)
		}
	}
}

func (rw *renderer) encode(b *strings.Builder, h map[string]any, indent int) {
	encs, _ := h["encodings"].(map[string]any)
	formats := sortedKeys(encs)
	if len(formats) == 0 {
		rw.warn("encode has no encodings")
		return
	}
	if pref, ok := h["prefer"].([]any); ok {
		var ordered []string
		seen := map[string]bool{}
		for _, p := range pref {
			s, _ := p.(string)
			if s == "" || seen[s] {
				continue
			}
			ordered = append(ordered, s)
			seen[s] = true
		}
		for _, f := range formats {
			if !seen[f] {
				ordered = append(ordered, f)
			}
		}
		if len(ordered) > 0 {
			formats = ordered
		}
	}
	var opts []string
	if v, ok := h["minimum_length"]; ok {
		opts = append(opts, "minimum_length "+scalar(v))
	}
	for _, f := range formats {
		if cfg, ok := encs[f].(map[string]any); ok {
			if lvl, ok := cfg["level"]; ok {
				opts = append(opts, f+" "+scalar(lvl))
			}
			for k := range cfg {
				if k != "level" {
					rw.warn("encode.%s field %q is not represented", f, k)
				}
			}
		}
	}
	for k := range h {
		if k != "handler" && k != "encodings" && k != "prefer" && k != "minimum_length" {
			rw.warn("encode field %q is not represented", k)
		}
	}
	b.WriteString(indentS(indent) + "encode " + strings.Join(formats, " "))
	if len(opts) == 0 {
		b.WriteString("\n")
		return
	}
	b.WriteString(" {\n")
	for _, o := range opts {
		b.WriteString(indentS(indent+1) + o + "\n")
	}
	b.WriteString(indentS(indent) + "}\n")
}

func (rw *renderer) staticResponse(b *strings.Builder, h map[string]any, indent int) {
	body, _ := h["body"].(string)
	status, hasStatus := h["status_code"]
	line := "respond"
	if body != "" {
		line += " " + token(body)
	}
	if hasStatus {
		line += " " + scalar(status)
	} else if body == "" {
		line += " 200"
	}
	b.WriteString(indentS(indent) + line + "\n")
	for k := range h {
		if k != "handler" && k != "status_code" && k != "body" {
			rw.warn("static_response field %q is not represented", k)
		}
	}
}

func (rw *renderer) matcher(r model.Route, top bool) (string, bool) {
	ms, _ := r["match"].([]any)
	if len(ms) == 0 {
		return "", false
	}
	if len(ms) > 1 {
		rw.warn("multiple matcher sets: only the first is rendered")
	}
	m, ok := ms[0].(map[string]any)
	if !ok {
		rw.warn("matcher set is not an object")
		return "", false
	}
	var lines []string
	for _, k := range sortedKeys(m) {
		switch k {
		case "host":
			if !top {
				rw.warn("nested host matcher is not represented")
				lines = append(lines, "# unsupported nested host matcher")
			}
		case "path":
			if vals := stringValues(m["path"]); len(vals) > 0 {
				lines = append(lines, "path "+joinTokens(vals))
			}
		case "header":
			hm, _ := m["header"].(map[string]any)
			for _, name := range sortedKeys(hm) {
				if vals := stringValues(hm[name]); len(vals) > 0 {
					lines = append(lines, "header "+token(name)+" "+joinTokens(vals))
				}
			}
		case "method":
			if vals := stringValues(m["method"]); len(vals) > 0 {
				lines = append(lines, "method "+joinTokens(vals))
			}
		case "query":
			qm, _ := m["query"].(map[string]any)
			for _, name := range sortedKeys(qm) {
				if vals := stringValues(qm[name]); len(vals) > 0 {
					lines = append(lines, "query "+token(name)+"="+joinTokens(vals))
				}
			}
		default:
			lines = append(lines, "# unsupported matcher "+strconv.Quote(k)+": "+compactJSON(m[k]))
			rw.warn("unsupported matcher field %q", k)
		}
	}
	if len(lines) == 0 {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

func (rw *renderer) nextMatcher() string {
	rw.matcherN++
	return fmt.Sprintf("@m%d", rw.matcherN)
}

func indentS(n int) string { return strings.Repeat("    ", n) }

func indentLines(s string, n int) string {
	pad := indentS(n)
	return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func stringValues(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	case string:
		return []string{t}
	default:
		return nil
	}
}

func joinTokens(vals []string) string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = token(v)
	}
	return strings.Join(out, " ")
}

func token(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-:/*+@%", r)
		if !ok {
			return strconv.Quote(s)
		}
	}
	return s
}

func scalar(v any) string {
	switch t := v.(type) {
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		return strconv.FormatBool(t)
	case string:
		return token(t)
	default:
		return fmt.Sprint(t)
	}
}

func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "?"
	}
	s := string(b)
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return s
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
