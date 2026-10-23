package model

import (
	"encoding/json"
	"sort"
	"strings"
)

// Change is one semantic JSON diff entry. Path is a JSON pointer.
type Change struct {
	Op     string `json:"op"` // add | remove | replace
	Path   string `json:"path"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

func jsonEqual(a, b any) bool {
	// Canonical comparison: json.Marshal sorts map keys and normalizes int /
	// json.Number representations.
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ab) == string(bb)
}

// Diff computes semantic add/remove/replace changes.
func Diff(before, after any) []Change {
	var out []Change
	diffValue("", before, after, true, true, &out)
	return out
}

// DiffRoute builds a preview diff for one route at its array index.
func DiffRoute(before, after *Route, index int) []Change {
	prefix := "/routes/" + itoa(index)
	if before == nil && after == nil {
		return []Change{}
	}
	if before == nil {
		return []Change{{Op: "add", Path: prefix, After: map[string]any(*after)}}
	}
	if after == nil {
		return []Change{{Op: "remove", Path: prefix, Before: map[string]any(*before)}}
	}
	out := []Change{}
	diffValue(prefix, map[string]any(*before), map[string]any(*after), true, true, &out)
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func diffValue(path string, before, after any, bOK, aOK bool, out *[]Change) {
	if !bOK && !aOK {
		return
	}
	if bOK && !aOK {
		*out = append(*out, Change{Op: "remove", Path: path, Before: before})
		return
	}
	if !bOK && aOK {
		*out = append(*out, Change{Op: "add", Path: path, After: after})
		return
	}
	bm, bIsMap := before.(map[string]any)
	am, aIsMap := after.(map[string]any)
	if bIsMap && aIsMap {
		keys := map[string]bool{}
		for k := range bm {
			keys[k] = true
		}
		for k := range am {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			bv, bHas := bm[k]
			av, aHas := am[k]
			diffValue(path+"/"+escapePointer(k), bv, av, bHas, aHas, out)
		}
		return
	}
	bs, bIsSlice := before.([]any)
	as, aIsSlice := after.([]any)
	if bIsSlice && aIsSlice {
		n := len(bs)
		if len(as) > n {
			n = len(as)
		}
		for i := 0; i < n; i++ {
			p := path + "/" + itoa(i)
			if i >= len(bs) {
				diffValue(p, nil, as[i], false, true, out)
			} else if i >= len(as) {
				diffValue(p, bs[i], nil, true, false, out)
			} else {
				diffValue(p, bs[i], as[i], true, true, out)
			}
		}
		return
	}
	if !jsonEqual(before, after) {
		*out = append(*out, Change{Op: "replace", Path: path, Before: before, After: after})
	}
}

func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// DefaultRedactPatterns is the spec §4.2 default list. Patterns are
// case-insensitive globs.
var DefaultRedactPatterns = []string{
	"*authorization*", "*token*", "*secret*", "*key*", "*cookie*", "*password*",
}

// HeaderMatches reports whether a header name matches one of the redact globs.
func HeaderMatches(name string, patterns []string) bool {
	lname := strings.ToLower(name)
	for _, p := range patterns {
		if globMatch(strings.ToLower(p), lname) {
			return true
		}
	}
	return false
}

func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// HeaderFieldFromPointer extracts a header name from a change pointer such as
// /routes/0/handle/1/request/set/Authorization/0.
func HeaderFieldFromPointer(pointer string) string {
	parts := strings.Split(pointer, "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "set" {
			return strings.ReplaceAll(strings.ReplaceAll(parts[i+1], "~1", "/"), "~0", "~")
		}
	}
	return ""
}

// Redact replaces sensitive values in a diff for display. Redaction is a
// presentation feature only (spec §4.2).
func Redact(changes []Change, patterns []string) []Change {
	if patterns == nil {
		patterns = DefaultRedactPatterns
	}
	out := make([]Change, len(changes))
	for i, c := range changes {
		out[i] = c
		if name := HeaderFieldFromPointer(c.Path); name != "" && HeaderMatches(name, patterns) {
			out[i].Before = redactValue(c.Before)
			out[i].After = redactValue(c.After)
		}
		// Whole-route add/remove diffs carry the full route object, so the
		// header sets inside it must be redacted structurally too.
		out[i].Before = redactDeep(out[i].Before, patterns)
		out[i].After = redactDeep(out[i].After, patterns)
	}
	return out
}

// RedactText hides exact sensitive parameter values in a display string.
func RedactText(s string, values []string) string {
	for _, v := range values {
		if v != "" {
			s = strings.ReplaceAll(s, v, "•••")
		}
	}
	return s
}

func redactDeep(v any, patterns []string) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			if k == "set" {
				if sm, ok := e.(map[string]any); ok {
					ns := make(map[string]any, len(sm))
					for name, vals := range sm {
						if HeaderMatches(name, patterns) {
							ns[name] = []any{"•••"}
						} else {
							ns[name] = redactDeep(vals, patterns)
						}
					}
					out[k] = ns
					continue
				}
			}
			out[k] = redactDeep(e, patterns)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = redactDeep(t[i], patterns)
		}
		return out
	default:
		return v
	}
}

// RedactValues hides exact sensitive parameter values (templates, spec §10).
func RedactValues(changes []Change, values []string) []Change {
	if len(values) == 0 {
		return changes
	}
	out := make([]Change, len(changes))
	for i, c := range changes {
		out[i] = c
		out[i].Before = redactStrings(c.Before, values)
		out[i].After = redactStrings(c.After, values)
	}
	return out
}

func redactStrings(v any, values []string) any {
	switch t := v.(type) {
	case string:
		for _, s := range values {
			if s != "" && strings.Contains(t, s) {
				return "•••"
			}
		}
		return t
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = redactStrings(t[i], values)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = redactStrings(e, values)
		}
		return out
	default:
		return v
	}
}

func redactValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = "•••"
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k := range t {
			out[k] = "•••"
		}
		return out
	default:
		return "•••"
	}
}
