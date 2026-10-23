package templates

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEmbedded(t *testing.T) {
	list, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 2 {
		t.Fatalf("expected embedded templates, got %d", len(list))
	}
	ids := map[string]bool{}
	for _, tmpl := range list {
		ids[tmpl.ID] = true
	}
	if !ids["mcphub"] || !ids["omniroute"] {
		t.Fatalf("missing defaults: %v", ids)
	}
}

func TestRenderMCPHub(t *testing.T) {
	tmpl, err := Get("", "mcphub")
	if err != nil {
		t.Fatal(err)
	}
	sp, reqs, err := tmpl.Render(map[string]string{"domain": "mcp.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if sp.Host != "mcp.example.com" || sp.Upstream != "127.0.0.1:3000" {
		t.Fatalf("bad render: %+v", sp)
	}
	if len(reqs) != 2 {
		t.Fatalf("requirements: %+v", reqs)
	}
	if _, _, err := tmpl.Render(map[string]string{}); err == nil {
		t.Fatal("required domain must be enforced")
	}
	if _, _, err := tmpl.Render(map[string]string{"domain": "not a domain"}); err == nil {
		t.Fatal("invalid domain must be rejected")
	}
	if _, _, err := tmpl.Render(map[string]string{"domain": "mcp.example.com", "upstream_port": "99999"}); err == nil {
		t.Fatal("invalid port must be rejected")
	}
}

func TestLocalOverrideAndUnknownPlaceholder(t *testing.T) {
	dir := t.TempDir()
	custom := `schema_version: 1
id: mcphub
name: Custom Hub
description: override
parameters:
  - name: domain
    type: domain
    required: true
route:
  host: "{{domain}}"
  upstream: "127.0.0.1:3999"
`
	if err := os.WriteFile(filepath.Join(dir, "mcphub.yaml"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	tmpl, err := Get(dir, "mcphub")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Name != "Custom Hub" {
		t.Fatalf("local file did not override: %+v", tmpl)
	}
	bad := `schema_version: 1
id: bad
name: Bad
parameters:
  - name: domain
    type: domain
    required: true
route:
  host: "{{nope}}"
  upstream: "127.0.0.1:1"
`
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	t2, err := Get(dir, "bad")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := t2.Render(map[string]string{"domain": "a.example.com"}); err == nil {
		t.Fatal("unknown placeholder must be rejected")
	}
	// Unknown YAML fields are rejected at load time.
	bad2 := `schema_version: 1
id: bad2
name: Bad2
exec: rm -rf /
parameters: []
route: {host: "a.example.com", upstream: "127.0.0.1:1"}
`
	if err := os.WriteFile(filepath.Join(dir, "bad2.yaml"), []byte(bad2), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("unknown field exec must be rejected")
	}
}
