// Package templates loads the declarative YAML route templates, validates
// parameters and renders a route spec (spec §10).
package templates

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	defaults "devproxy/templates"

	"devproxy/internal/config"
	"devproxy/internal/model"
	"gopkg.in/yaml.v3"
)

type Parameter struct {
	Name        string `yaml:"name" json:"name"`
	Type        string `yaml:"type" json:"type"`
	Required    bool   `yaml:"required" json:"required"`
	Default     any    `yaml:"default" json:"default,omitempty"`
	Sensitive   bool   `yaml:"sensitive" json:"sensitive,omitempty"`
	Description string `yaml:"description" json:"description,omitempty"`
}

type HeaderSet struct {
	Set    map[string]string `yaml:"set" json:"set,omitempty"`
	Delete []string          `yaml:"delete" json:"delete,omitempty"`
}

type RouteTemplate struct {
	Host               string    `yaml:"host" json:"host"`
	Upstream           string    `yaml:"upstream" json:"upstream"`
	PathPrefix         string    `yaml:"path_prefix" json:"path_prefix"`
	StripPrefix        bool      `yaml:"strip_prefix" json:"strip_prefix"`
	UpstreamTLS        bool      `yaml:"upstream_tls" json:"upstream_tls"`
	TLSServerName      string    `yaml:"tls_server_name" json:"tls_server_name"`
	InsecureSkipVerify bool      `yaml:"insecure_skip_verify" json:"insecure_skip_verify"`
	RequestHeaders     HeaderSet `yaml:"request_headers" json:"request_headers"`
	ResponseHeaders    HeaderSet `yaml:"response_headers" json:"response_headers"`
}

type Template struct {
	SchemaVersion int           `yaml:"schema_version" json:"schema_version"`
	ID            string        `yaml:"id" json:"id"`
	Name          string        `yaml:"name" json:"name"`
	Description   string        `yaml:"description" json:"description"`
	Parameters    []Parameter   `yaml:"parameters" json:"parameters"`
	Requirements  []string      `yaml:"requirements" json:"requirements"`
	Route         RouteTemplate `yaml:"route" json:"route"`
}

var paramRE = regexp.MustCompile(`\{\{([a-zA-Z0-9_]+)\}\}`)

// Load returns embedded defaults overridden by YAML files in dir.
func Load(dir string) ([]Template, error) {
	byID := map[string]Template{}
	entries, err := fs.ReadDir(defaults.Defaults, ".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(defaults.Defaults, e.Name())
		if err != nil {
			return nil, err
		}
		t, err := parse(data)
		if err != nil {
			return nil, fmt.Errorf("embedded %s: %w", e.Name(), err)
		}
		byID[t.ID] = t
	}
	if dir != "" {
		list, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		for _, e := range list {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			t, err := parse(data)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", e.Name(), err)
			}
			byID[t.ID] = t // local wins
		}
	}
	out := make([]Template, 0, len(byID))
	for _, t := range byID {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func Get(dir, id string) (*Template, error) {
	list, err := Load(dir)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("template %q not found", id)
}

func parse(data []byte) (Template, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var t Template
	if err := dec.Decode(&t); err != nil {
		return t, err
	}
	if err := t.Validate(); err != nil {
		return t, err
	}
	return t, nil
}

func (t *Template) Validate() error {
	if t.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema_version %d", t.SchemaVersion)
	}
	if strings.TrimSpace(t.ID) == "" || strings.TrimSpace(t.Name) == "" {
		return errors.New("id and name are required")
	}
	if t.Route.Host == "" || t.Route.Upstream == "" {
		return errors.New("route.host and route.upstream are required")
	}
	seen := map[string]bool{}
	for i := range t.Parameters {
		p := &t.Parameters[i]
		if p.Name == "" {
			return errors.New("parameter name is required")
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate parameter %q", p.Name)
		}
		seen[p.Name] = true
		if err := validateType(p.Type); err != nil {
			return fmt.Errorf("parameter %q: %w", p.Name, err)
		}
	}
	return nil
}

func validateType(typ string) error {
	switch {
	case typ == "string", typ == "domain", typ == "hostport", typ == "port", typ == "bool":
		return nil
	case strings.HasPrefix(typ, "enum(") && strings.HasSuffix(typ, ")"):
		if len(strings.Split(strings.TrimSuffix(strings.TrimPrefix(typ, "enum("), ")"), ",")) == 0 {
			return errors.New("empty enum")
		}
		return nil
	default:
		return fmt.Errorf("unknown type %q", typ)
	}
}

// Render validates parameters and substitutes them into the route template.
func (t *Template) Render(in map[string]string) (model.Spec, []string, error) {
	values := map[string]string{}
	for _, p := range t.Parameters {
		v, ok := in[p.Name]
		if !ok || v == "" {
			if p.Default != nil {
				v = fmt.Sprint(p.Default)
			} else if p.Required {
				return model.Spec{}, nil, fmt.Errorf("parameter %q is required", p.Name)
			}
		}
		if err := validateValue(p, v); err != nil {
			return model.Spec{}, nil, err
		}
		values[p.Name] = v
	}
	sub := func(s string) (string, error) {
		var missing string
		out := paramRE.ReplaceAllStringFunc(s, func(m string) string {
			name := m[2 : len(m)-2]
			v, ok := values[name]
			if !ok {
				missing = name
				return m
			}
			return v
		})
		if missing != "" {
			return "", fmt.Errorf("template references unknown parameter %q", missing)
		}
		return out, nil
	}
	host, err := sub(t.Route.Host)
	if err != nil {
		return model.Spec{}, nil, err
	}
	upstream, err := sub(t.Route.Upstream)
	if err != nil {
		return model.Spec{}, nil, err
	}
	pathPrefix, err := sub(t.Route.PathPrefix)
	if err != nil {
		return model.Spec{}, nil, err
	}
	tlsName, err := sub(t.Route.TLSServerName)
	if err != nil {
		return model.Spec{}, nil, err
	}
	reqSet, err := toSets(t.Route.RequestHeaders.Set, sub)
	if err != nil {
		return model.Spec{}, nil, err
	}
	respSet, err := toSets(t.Route.ResponseHeaders.Set, sub)
	if err != nil {
		return model.Spec{}, nil, err
	}
	strip := ""
	if t.Route.StripPrefix {
		strip = strings.TrimSuffix(pathPrefix, "*")
	}
	sp := model.Spec{
		Host:               host,
		Upstream:           upstream,
		Path:               pathMatcher(pathPrefix),
		StripPrefix:        strip,
		UpstreamTLS:        t.Route.UpstreamTLS,
		TLSServerName:      tlsName,
		InsecureSkipVerify: t.Route.InsecureSkipVerify,
		RequestSet:         reqSet,
		RequestDelete:      t.Route.RequestHeaders.Delete,
		ResponseSet:        respSet,
		ResponseDelete:     t.Route.ResponseHeaders.Delete,
	}
	if sp.InsecureSkipVerify && !sp.UpstreamTLS {
		return model.Spec{}, nil, errors.New("insecure_skip_verify requires upstream_tls")
	}
	if err := sp.Validate(); err != nil {
		return model.Spec{}, nil, err
	}
	reqs := make([]string, 0, len(t.Requirements))
	for _, r := range t.Requirements {
		s, err := sub(r)
		if err != nil {
			return model.Spec{}, nil, err
		}
		reqs = append(reqs, s)
	}
	return sp, reqs, nil
}

func toSets(in map[string]string, sub func(string) (string, error)) (map[string][]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		rendered, err := sub(v)
		if err != nil {
			return nil, err
		}
		out[k] = []string{rendered}
	}
	return out, nil
}

func pathMatcher(prefix string) string { return model.PathPrefixMatcher(prefix) }

func validateValue(p Parameter, v string) error {
	if v == "" && !p.Required {
		return nil
	}
	switch {
	case p.Type == "string":
		if strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("parameter %q contains a control character", p.Name)
		}
	case p.Type == "domain":
		if err := config.ValidateDomain(v); err != nil {
			return fmt.Errorf("parameter %q: %w", p.Name, err)
		}
	case p.Type == "hostport":
		if _, _, err := net.SplitHostPort(v); err != nil {
			if strings.Contains(v, ":") {
				return fmt.Errorf("parameter %q must be host[:port]", p.Name)
			}
		}
	case p.Type == "port":
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("parameter %q must be a port (1..65535)", p.Name)
		}
	case p.Type == "bool":
		if _, err := strconv.ParseBool(v); err != nil {
			return fmt.Errorf("parameter %q must be a boolean", p.Name)
		}
	case strings.HasPrefix(p.Type, "enum("):
		allowed := strings.Split(strings.TrimSuffix(strings.TrimPrefix(p.Type, "enum("), ")"), ",")
		for _, a := range allowed {
			if strings.TrimSpace(a) == v {
				return nil
			}
		}
		return fmt.Errorf("parameter %q must be one of %s", p.Name, strings.Join(allowed, ","))
	}
	return nil
}

// SensitiveValues returns the concrete values of sensitive parameters.
func (t *Template) SensitiveValues(in map[string]string) []string {
	var out []string
	for _, p := range t.Parameters {
		if !p.Sensitive {
			continue
		}
		v := in[p.Name]
		if v == "" && p.Default != nil {
			v = fmt.Sprint(p.Default)
		}
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
