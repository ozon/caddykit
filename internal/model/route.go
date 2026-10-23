// Package model holds the canonical route representation, identity helpers,
// classification, normalization, diffing and collision detection.
package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"caddykit/internal/config"
	"github.com/google/uuid"
)

// Route is one Caddy route object. Maps (instead of typed structs) preserve
// unknown fields losslessly, as required for form edits (spec §7.2).
type Route map[string]any

type Class string

const (
	FormEditable Class = "FORM_EDITABLE"
	JSONOnly     Class = "JSON_ONLY"
)

type Reason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r *Reason) Error() string { return r.Code + ": " + r.Message }

func reason(code, msg string) *Reason { return &Reason{Code: code, Message: msg} }

// Reason codes (machine readable).
const (
	RNoMatch              = "NO_MATCH"
	RMultipleMatch        = "MULTIPLE_MATCH"
	RMultipleHosts        = "MULTIPLE_HOSTS"
	RUnsupportedMatcher   = "UNSUPPORTED_MATCHER"
	RNoHandler            = "NO_HANDLER"
	RUnsupportedHandler   = "UNSUPPORTED_HANDLER"
	RHandlerOrder         = "HANDLER_ORDER"
	RMultipleRewrite      = "MULTIPLE_REWRITE"
	RMultipleHeaders      = "MULTIPLE_HEADERS"
	RMultipleProxy        = "MULTIPLE_PROXY"
	RMultipleUpstreams    = "MULTIPLE_UPSTREAMS"
	RUnsupportedUpstream  = "UNSUPPORTED_UPSTREAM"
	RUnsupportedTransport = "UNSUPPORTED_TRANSPORT"
	RUnsupportedField     = "UNSUPPORTED_FIELD"
	RTerminalFalse        = "TERMINAL_FALSE"
	RInvalid              = "INVALID_ROUTE"
)

// NewID returns the identity for newly managed routes (spec §2.1).
func NewID() string { return "caddykit/r/" + uuid.Must(uuid.NewV7()).String() }

// Decode parses a route with json.Number preserved.
func Decode(data []byte) (Route, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("route is not a JSON object")
	}
	return Route(m), nil
}

func DecodeList(data []byte) ([]Route, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v []any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	out := make([]Route, 0, len(v))
	for i, e := range v {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("route %d is not a JSON object", i)
		}
		out = append(out, Route(m))
	}
	return out, nil
}

func Clone(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[k] = Clone(e)
		}
		return m
	case Route:
		return Route(Clone(map[string]any(t)).(map[string]any))
	case []any:
		s := make([]any, len(t))
		for i, e := range t {
			s[i] = Clone(e)
		}
		return s
	default:
		return v
	}
}

func (r Route) Clone() Route {
	return Route(Clone(map[string]any(r)).(map[string]any))
}

func (r Route) String() string {
	b, _ := json.Marshal(r)
	return string(b)
}

func (r Route) ID() string {
	s, _ := r["@id"].(string)
	return s
}

func (r Route) Terminal() bool {
	b, _ := r["terminal"].(bool)
	return b
}

// Hosts returns all host matcher patterns. No host matcher means match-all.
func (r Route) Hosts() []string {
	var out []string
	for _, m := range matchers(r) {
		if hs, ok := m["host"].([]any); ok {
			for _, h := range hs {
				if s, ok := h.(string); ok {
					out = append(out, NormalizeHost(s))
				}
			}
		}
	}
	if len(out) == 0 {
		out = []string{"*"}
	}
	return out
}

// Paths returns all path matcher patterns. Empty means match-all.
func (r Route) Paths() []string {
	var out []string
	for _, m := range matchers(r) {
		if ps, ok := m["path"].([]any); ok {
			for _, p := range ps {
				if s, ok := p.(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

func matchers(r Route) []map[string]any {
	arr, _ := r["match"].([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func (r Route) UpstreamDial() string {
	arr, _ := r["handle"].([]any)
	for _, e := range arr {
		h, ok := e.(map[string]any)
		if !ok || h["handler"] != "reverse_proxy" {
			continue
		}
		ups, _ := h["upstreams"].([]any)
		if len(ups) == 0 {
			return ""
		}
		u, _ := ups[0].(map[string]any)
		s, _ := u["dial"].(string)
		return s
	}
	return ""
}

// Spec is the form-editable subset of a route (spec §2.3, §2.4).
type Spec struct {
	ID                 string              `json:"id"`
	Host               string              `json:"host"`
	Path               string              `json:"path"`
	Upstream           string              `json:"upstream"`
	UpstreamTLS        bool                `json:"upstream_tls"`
	TLSServerName      string              `json:"tls_server_name"`
	InsecureSkipVerify bool                `json:"insecure_skip_verify"`
	StripPrefix        string              `json:"strip_prefix"`
	RequestSet         map[string][]string `json:"request_set"`
	RequestDelete      []string            `json:"request_delete"`
	ResponseSet        map[string][]string `json:"response_set"`
	ResponseDelete     []string            `json:"response_delete"`
	ResponseDeferred   bool                `json:"response_deferred"`
	ExtraTLS           map[string]any      `json:"extra_tls,omitempty"`
}

func (s Spec) Build() Route {
	match := map[string]any{"host": []any{s.Host}}
	if s.Path != "" {
		match["path"] = []any{s.Path}
	}
	handle := []any{}
	if s.StripPrefix != "" {
		handle = append(handle, map[string]any{
			"handler":           "rewrite",
			"strip_path_prefix": s.StripPrefix,
		})
	}
	if len(s.RequestSet) > 0 || len(s.RequestDelete) > 0 || len(s.ResponseSet) > 0 || len(s.ResponseDelete) > 0 {
		h := map[string]any{"handler": "headers"}
		if len(s.RequestSet) > 0 || len(s.RequestDelete) > 0 {
			req := map[string]any{}
			if len(s.RequestSet) > 0 {
				req["set"] = stringSliceMap(s.RequestSet)
			}
			if len(s.RequestDelete) > 0 {
				req["delete"] = toAnyStrings(s.RequestDelete)
			}
			h["request"] = req
		}
		if len(s.ResponseSet) > 0 || len(s.ResponseDelete) > 0 {
			resp := map[string]any{}
			if len(s.ResponseSet) > 0 {
				resp["set"] = stringSliceMap(s.ResponseSet)
			}
			if len(s.ResponseDelete) > 0 {
				resp["delete"] = toAnyStrings(s.ResponseDelete)
			}
			if s.ResponseDeferred {
				resp["deferred"] = true
			}
			h["response"] = resp
		}
		handle = append(handle, h)
	}
	rp := map[string]any{
		"handler":   "reverse_proxy",
		"upstreams": []any{map[string]any{"dial": s.Upstream}},
	}
	if s.UpstreamTLS {
		tls := map[string]any{}
		for k, v := range s.ExtraTLS {
			tls[k] = Clone(v)
		}
		if s.TLSServerName != "" {
			tls["server_name"] = s.TLSServerName
		}
		if s.InsecureSkipVerify {
			tls["insecure_skip_verify"] = true
		}
		rp["transport"] = map[string]any{"protocol": "http", "tls": tls}
	}
	handle = append(handle, rp)

	r := Route{
		"match":    []any{match},
		"handle":   handle,
		"terminal": true,
	}
	if s.ID != "" {
		r["@id"] = s.ID
	}
	return r
}

func stringSliceMap(in map[string][]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = toAnyStrings(v)
	}
	return out
}

func toAnyStrings(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func anyStrings(v any) ([]string, bool) {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	case []string:
		return t, true
	default:
		return nil, false
	}
}

// Extract validates the canonical form and returns the form spec.
func Extract(r Route) (Spec, Class, *Reason) {
	var s Spec
	class := FormOnly(r)
	if class != nil {
		return s, JSONOnly, class
	}
	s.ID = r.ID()

	ms := matchers(r)
	m := ms[0]
	hosts, _ := anyStrings(m["host"])
	s.Host = NormalizeHost(hosts[0])
	if ps, ok := m["path"]; ok {
		paths, _ := anyStrings(ps)
		if len(paths) > 0 {
			s.Path = paths[0]
		}
	}

	arr, _ := r["handle"].([]any)
	seen := map[string]bool{}
	for _, e := range arr {
		h := e.(map[string]any)
		switch h["handler"] {
		case "rewrite":
			s.StripPrefix, _ = h["strip_path_prefix"].(string)
			seen["rewrite"] = true
		case "headers":
			seen["headers"] = true
			if req, ok := h["request"].(map[string]any); ok {
				if set, ok := req["set"].(map[string]any); ok {
					s.RequestSet = parseSet(set)
				}
				if del, ok := req["delete"]; ok {
					s.RequestDelete, _ = anyStrings(del)
				}
			}
			if resp, ok := h["response"].(map[string]any); ok {
				if set, ok := resp["set"].(map[string]any); ok {
					s.ResponseSet = parseSet(set)
				}
				if del, ok := resp["delete"]; ok {
					s.ResponseDelete, _ = anyStrings(del)
				}
				s.ResponseDeferred, _ = resp["deferred"].(bool)
			}
		case "reverse_proxy":
			ups := h["upstreams"].([]any)
			s.Upstream = ups[0].(map[string]any)["dial"].(string)
			if tr, ok := h["transport"].(map[string]any); ok {
				s.UpstreamTLS = true
				if tls, ok := tr["tls"].(map[string]any); ok {
					s.TLSServerName, _ = tls["server_name"].(string)
					s.InsecureSkipVerify, _ = tls["insecure_skip_verify"].(bool)
					for k, v := range tls {
						if k != "server_name" && k != "insecure_skip_verify" {
							if s.ExtraTLS == nil {
								s.ExtraTLS = map[string]any{}
							}
							s.ExtraTLS[k] = Clone(v)
						}
					}
				}
			}
		}
	}
	_ = seen
	return s, FormEditable, nil
}

func parseSet(m map[string]any) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		vals, _ := anyStrings(v)
		out[k] = vals
	}
	return out
}

// FormOnly returns nil when the route matches the supported canonical form.
func FormOnly(r Route) *Reason {
	for k := range r {
		switch k {
		case "@id", "match", "handle", "terminal":
		default:
			return reason(RUnsupportedField, fmt.Sprintf("unsupported route field %q", k))
		}
	}
	if t, ok := r["terminal"]; ok {
		if _, ok := t.(bool); !ok {
			return reason(RUnsupportedField, "terminal must be a boolean")
		}
	}
	if !r.Terminal() {
		return reason(RTerminalFalse, "terminal must be true for form editing; edit this route as JSON")
	}
	if id, ok := r["@id"]; ok {
		if _, ok := id.(string); !ok {
			return reason(RUnsupportedField, "@id must be a string")
		}
	}
	mv, ok := r["match"]
	if !ok || mv == nil {
		return reason(RNoMatch, "route has no match block")
	}
	ms, _ := mv.([]any)
	if len(ms) != 1 {
		return reason(RMultipleMatch, "exactly one matcher is supported")
	}
	m, ok := ms[0].(map[string]any)
	if !ok {
		return reason(RUnsupportedMatcher, "matcher is not an object")
	}
	for k := range m {
		if k != "host" && k != "path" {
			return reason(RUnsupportedMatcher, fmt.Sprintf("unsupported matcher %q", k))
		}
	}
	hosts, ok := anyStrings(m["host"])
	if !ok || len(hosts) != 1 {
		return reason(RMultipleHosts, "exactly one host is supported")
	}
	if _, ok := m["path"]; ok {
		paths, ok := anyStrings(m["path"])
		if !ok || len(paths) > 1 {
			return reason(RUnsupportedMatcher, "at most one path is supported")
		}
	}
	hv, ok := r["handle"].([]any)
	if !ok || len(hv) == 0 {
		return reason(RNoHandler, "route has no handler")
	}
	if len(hv) > 3 {
		return reason(RUnsupportedHandler, "too many handlers")
	}
	var order []string
	for _, e := range hv {
		h, ok := e.(map[string]any)
		if !ok {
			return reason(RUnsupportedHandler, "handler is not an object")
		}
		name, _ := h["handler"].(string)
		order = append(order, name)
		switch name {
		case "rewrite":
			for k := range h {
				if k != "handler" && k != "strip_path_prefix" {
					return reason(RUnsupportedField, fmt.Sprintf("unsupported rewrite field %q", k))
				}
			}
			if _, ok := h["strip_path_prefix"].(string); !ok {
				return reason(RUnsupportedField, "strip_path_prefix must be a string")
			}
		case "headers":
			if err := validateHeadersHandler(h); err != nil {
				return err
			}
		case "reverse_proxy":
			if err := validateProxyHandler(h); err != nil {
				return err
			}
		default:
			return reason(RUnsupportedHandler, fmt.Sprintf("unsupported handler %q", name))
		}
	}
	// Fixed order: rewrite? headers? reverse_proxy (last).
	for i := 1; i < len(order); i++ {
		if order[i] == "rewrite" {
			return reason(RHandlerOrder, "rewrite must come before headers and reverse_proxy")
		}
		if order[i] == "headers" && order[i-1] != "rewrite" {
			return reason(RMultipleHeaders, "duplicate headers handler")
		}
	}
	count := map[string]int{}
	for _, n := range order {
		count[n]++
	}
	if count["rewrite"] > 1 {
		return reason(RMultipleRewrite, "duplicate rewrite handler")
	}
	if count["headers"] > 1 {
		return reason(RMultipleHeaders, "duplicate headers handler")
	}
	if count["reverse_proxy"] != 1 {
		return reason(RMultipleProxy, "exactly one reverse_proxy handler is required")
	}
	if order[len(order)-1] != "reverse_proxy" {
		return reason(RHandlerOrder, "reverse_proxy must be the last handler")
	}
	return nil
}

func validateHeadersHandler(h map[string]any) *Reason {
	for k := range h {
		if k != "handler" && k != "request" && k != "response" {
			return reason(RUnsupportedField, fmt.Sprintf("unsupported headers field %q", k))
		}
	}
	check := func(v any, response bool) *Reason {
		if v == nil {
			return nil
		}
		m, ok := v.(map[string]any)
		if !ok {
			return reason(RUnsupportedField, "header block is not an object")
		}
		for k := range m {
			if k != "set" && k != "delete" && (!response || k != "deferred") {
				return reason(RUnsupportedField, fmt.Sprintf("unsupported header field %q", k))
			}
		}
		if set, ok := m["set"]; ok {
			sm, ok := set.(map[string]any)
			if !ok {
				return reason(RUnsupportedField, "header set must be an object")
			}
			for name, vals := range sm {
				if _, ok := anyStrings(vals); !ok {
					return reason(RUnsupportedField, "header values must be string arrays")
				}
				if !ValidHeaderName(name) {
					return reason(RUnsupportedField, "invalid header name")
				}
			}
		}
		if del, ok := m["delete"]; ok {
			vals, ok := anyStrings(del)
			if !ok {
				return reason(RUnsupportedField, "header delete must be a string array")
			}
			for _, name := range vals {
				if !ValidHeaderName(name) {
					return reason(RUnsupportedField, "invalid header name")
				}
			}
		}
		if d, ok := m["deferred"]; ok {
			if _, ok := d.(bool); !ok {
				return reason(RUnsupportedField, "deferred must be a boolean")
			}
		}
		return nil
	}
	if err := check(h["request"], false); err != nil {
		return err
	}
	return check(h["response"], true)
}

func validateProxyHandler(h map[string]any) *Reason {
	for k := range h {
		if k != "handler" && k != "upstreams" && k != "transport" {
			return reason(RUnsupportedField, fmt.Sprintf("unsupported reverse_proxy field %q", k))
		}
	}
	ups, ok := h["upstreams"].([]any)
	if !ok || len(ups) != 1 {
		return reason(RMultipleUpstreams, "exactly one upstream is supported")
	}
	u, ok := ups[0].(map[string]any)
	if !ok {
		return reason(RUnsupportedUpstream, "upstream is not an object")
	}
	for k := range u {
		if k != "dial" {
			return reason(RUnsupportedUpstream, fmt.Sprintf("unsupported upstream field %q", k))
		}
	}
	if d, ok := u["dial"].(string); !ok || d == "" {
		return reason(RUnsupportedUpstream, "upstream dial must be a non-empty string")
	}
	if tr, ok := h["transport"]; ok {
		tm, ok := tr.(map[string]any)
		if !ok {
			return reason(RUnsupportedTransport, "transport is not an object")
		}
		for k := range tm {
			if k != "protocol" && k != "tls" {
				return reason(RUnsupportedTransport, fmt.Sprintf("unsupported transport field %q", k))
			}
		}
		if p, ok := tm["protocol"].(string); !ok || p != "http" {
			return reason(RUnsupportedTransport, "only the http transport is supported")
		}
		if tls, ok := tm["tls"]; ok {
			tt, ok := tls.(map[string]any)
			if !ok {
				return reason(RUnsupportedTransport, "tls is not an object")
			}
			if v, ok := tt["server_name"]; ok {
				if _, ok := v.(string); !ok {
					return reason(RUnsupportedTransport, "server_name must be a string")
				}
			}
			if v, ok := tt["insecure_skip_verify"]; ok {
				if _, ok := v.(bool); !ok {
					return reason(RUnsupportedTransport, "insecure_skip_verify must be a boolean")
				}
			}
			// Unknown TLS fields are preserved (spec §2.4) and therefore allowed.
		}
	}
	return nil
}

func ValidHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", r)
		if !valid {
			return false
		}
	}
	return true
}

func ValidHeaderValue(v string) bool {
	return !strings.ContainsAny(v, "\r\n\x00")
}

// Validate checks the form spec before it may be written.
func (s *Spec) Validate() error {
	if err := ValidateHost(s.Host); err != nil {
		return fmt.Errorf("host: %w", err)
	}
	if s.Path != "" {
		if !strings.HasPrefix(s.Path, "/") || strings.ContainsAny(s.Path, " \t\r\n") {
			return fmt.Errorf("path must start with / and contain no whitespace")
		}
	}
	if s.StripPrefix != "" && !strings.HasPrefix(s.StripPrefix, "/") {
		return fmt.Errorf("strip prefix must start with /")
	}
	if err := ValidateUpstream(s.Upstream); err != nil {
		return err
	}
	if s.TLSServerName != "" {
		if strings.ContainsAny(s.TLSServerName, " \t\r\n") {
			return fmt.Errorf("tls server name contains whitespace")
		}
	}
	for _, set := range []map[string][]string{s.RequestSet, s.ResponseSet} {
		for name, vals := range set {
			if !ValidHeaderName(name) {
				return fmt.Errorf("invalid header name %q", name)
			}
			for _, v := range vals {
				if !ValidHeaderValue(v) {
					return fmt.Errorf("invalid value for header %q", name)
				}
			}
		}
	}
	for _, del := range [][]string{s.RequestDelete, s.ResponseDelete} {
		for _, name := range del {
			if !ValidHeaderName(name) {
				return fmt.Errorf("invalid header name %q", name)
			}
		}
	}
	if s.InsecureSkipVerify && !s.UpstreamTLS {
		return fmt.Errorf("insecure_skip_verify requires upstream TLS")
	}
	return nil
}

// PathPrefixMatcher turns a path prefix into a Caddy path matcher.
func PathPrefixMatcher(prefix string) string {
	if prefix == "" {
		return ""
	}
	if strings.Contains(prefix, "*") {
		return prefix
	}
	if strings.HasSuffix(prefix, "/") {
		return prefix + "*"
	}
	return prefix + "/*"
}

func ValidateHost(h string) error {
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	if strings.HasPrefix(h, "*.") {
		return config.ValidateDomain(h[2:])
	}
	return config.ValidateDomain(h)
}

func ValidateUpstream(u string) error {
	host, port, err := net.SplitHostPort(u)
	if err != nil {
		return fmt.Errorf("upstream must be host:port: %w", err)
	}
	if host == "" {
		return fmt.Errorf("upstream host is empty")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("upstream port must be 1..65535")
	}
	return nil
}

// Normalize returns a copy with defaults added, empty no-op fields removed and
// stable semantics for comparisons.
func Normalize(r Route) Route {
	c := r.Clone()
	if _, ok := c["terminal"]; !ok {
		c["terminal"] = false
	}
	if m, ok := c["match"].([]any); ok {
		arr := make([]any, 0, len(m))
		for _, e := range m {
			mm, ok := e.(map[string]any)
			if !ok {
				arr = append(arr, e)
				continue
			}
			if ps, ok := mm["path"].([]any); ok && len(ps) == 0 {
				delete(mm, "path")
			}
			if len(mm) > 0 {
				arr = append(arr, mm)
			}
		}
		if len(arr) == 0 {
			delete(c, "match")
		} else {
			c["match"] = arr
		}
	}
	if hs, ok := c["handle"].([]any); ok {
		for _, e := range hs {
			h, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if h["handler"] == "reverse_proxy" {
				if _, ok := h["transport"]; !ok {
					h["transport"] = map[string]any{"protocol": "http"}
				}
			}
			if h["handler"] == "headers" {
				for _, dir := range []string{"request", "response"} {
					d, ok := h[dir].(map[string]any)
					if !ok {
						continue
					}
					for _, k := range []string{"set", "delete"} {
						if v, ok := d[k]; ok {
							if isEmpty(v) {
								delete(d, k)
							}
						}
					}
					if len(d) == 0 {
						delete(h, dir)
					}
				}
			}
		}
	}
	if tls, ok := c["transport"].(map[string]any); ok && isEmpty(tls) {
		delete(c, "transport")
	}
	return c
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	case string:
		return t == ""
	default:
		return false
	}
}

// Equal compares two routes semantically (normalized, order-independent keys).
func Equal(a, b Route) bool {
	ab, err1 := json.Marshal(Normalize(a))
	bb, err2 := json.Marshal(Normalize(b))
	return err1 == nil && err2 == nil && bytes.Equal(ab, bb)
}

// MaxRouteDepth bounds recursive subroute validation and Caddyfile previews.
const MaxRouteDepth = 50

// ValidateJSONRoute validates a route edited through the JSON editor. It
// recurses into subroute handlers so nested routes cannot be saved empty.
func ValidateJSONRoute(r Route, expectedID string) error {
	return validateJSONRoute(r, expectedID, 0, "route")
}

func validateJSONRoute(r Route, expectedID string, depth int, path string) error {
	if depth > MaxRouteDepth {
		return fmt.Errorf("%s: nesting exceeds %d levels", path, MaxRouteDepth)
	}
	if len(r) == 0 {
		return fmt.Errorf("%s must not be empty", path)
	}
	if id, ok := r["@id"]; ok {
		s, ok := id.(string)
		if !ok {
			return fmt.Errorf("%s.@id must be a string", path)
		}
		if expectedID != "" && s != expectedID {
			return fmt.Errorf("%s.@id must not change (expected %q)", path, expectedID)
		}
	} else if expectedID != "" {
		return fmt.Errorf("%s.@id must not be removed", path)
	}
	if m, ok := r["match"]; ok {
		if _, ok := m.([]any); !ok {
			return fmt.Errorf("%s.match must be an array", path)
		}
	}
	h, ok := r["handle"].([]any)
	if !ok || len(h) == 0 {
		return fmt.Errorf("%s.handle must be a non-empty array", path)
	}
	for i, e := range h {
		hm, ok := e.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.handle[%d] must be an object", path, i)
		}
		name, _ := hm["handler"].(string)
		if name == "" {
			return fmt.Errorf("%s.handle[%d] has no handler", path, i)
		}
		if name != "subroute" {
			continue
		}
		routes, ok := hm["routes"].([]any)
		if !ok {
			return fmt.Errorf("%s.handle[%d].routes must be an array", path, i)
		}
		for j, re := range routes {
			rm, ok := re.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.handle[%d].routes[%d] must be an object", path, i, j)
			}
			if err := validateJSONRoute(Route(rm), "", depth+1, fmt.Sprintf("%s.handle[%d].routes[%d]", path, i, j)); err != nil {
				return err
			}
		}
	}
	if t, ok := r["terminal"]; ok {
		if _, ok := t.(bool); !ok {
			return fmt.Errorf("%s.terminal must be a boolean", path)
		}
	}
	return nil
}

// HasInsecureSkipVerify reports whether a route disables upstream certificate
// verification anywhere in its JSON.
func HasInsecureSkipVerify(v any) bool {
	switch t := v.(type) {
	case Route:
		return HasInsecureSkipVerify(map[string]any(t))
	case map[string]any:
		for k, e := range t {
			if k == "insecure_skip_verify" {
				if b, ok := e.(bool); ok && b {
					return true
				}
			}
			if HasInsecureSkipVerify(e) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if HasInsecureSkipVerify(e) {
				return true
			}
		}
	}
	return false
}

// DuplicateIDs returns IDs that occur more than once.
func DuplicateIDs(routes []Route) map[string]int {
	seen := map[string]int{}
	dup := map[string]int{}
	for _, r := range routes {
		id := r.ID()
		if id == "" {
			continue
		}
		seen[id]++
		if seen[id] == 2 {
			dup[id] = seen[id]
		} else if seen[id] > 2 {
			dup[id] = seen[id]
		}
	}
	return dup
}

func NormalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// HostEqual compares a matcher pattern to a concrete host for selection.
func HostEqual(pattern, host string) bool {
	return NormalizeHost(pattern) == NormalizeHost(host)
}

// HostsCollide reports whether two host matchers can match a common host.
func HostsCollide(a, b string) bool {
	a, b = NormalizeHost(a), NormalizeHost(b)
	if a == "*" || b == "*" {
		return true
	}
	if a == b {
		return true
	}
	aw, abase := strings.HasPrefix(a, "*."), strings.TrimPrefix(a, "*.")
	bw, bbase := strings.HasPrefix(b, "*."), strings.TrimPrefix(b, "*.")
	switch {
	case aw && bw:
		return abase == bbase || strings.HasSuffix(abase, "."+bbase) || strings.HasSuffix(bbase, "."+abase)
	case aw:
		return strings.HasSuffix(b, "."+abase)
	case bw:
		return strings.HasSuffix(a, "."+bbase)
	default:
		return false
	}
}

// PathsOverlap reports whether two Caddy path patterns can match a common path.
// An empty pattern matches everything; a trailing * is a prefix match.
func PathsOverlap(a, b string) bool {
	if a == "" || b == "" || a == "*" || b == "*" {
		return true
	}
	a = strings.TrimSuffix(a, "*")
	b = strings.TrimSuffix(b, "*")
	if a == "" || b == "" {
		return true
	}
	return prefixOverlap(a, b) || prefixOverlap(b, a)
}

func prefixOverlap(short, long string) bool {
	if short == long || short == "" {
		return true
	}
	if !strings.HasPrefix(long, short) {
		return false
	}
	return strings.HasSuffix(short, "/") || long[len(short)] == '/'
}

// RoutesCollide reports a routing collision between two route objects.
func RoutesCollide(a, b Route) bool {
	hosts := false
	for _, ha := range a.Hosts() {
		for _, hb := range b.Hosts() {
			if HostsCollide(ha, hb) {
				hosts = true
				break
			}
		}
	}
	if !hosts {
		return false
	}
	pa, pb := a.Paths(), b.Paths()
	if len(pa) == 0 || len(pb) == 0 {
		return true
	}
	for _, a := range pa {
		for _, b := range pb {
			if PathsOverlap(a, b) {
				return true
			}
		}
	}
	return false
}

// SortStrings returns a sorted copy (used for stable candidate lists).
func SortStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
