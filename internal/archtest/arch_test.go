package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoCaddyfileOrServiceControl enforces acceptance criterion 3: devproxy
// never reads or applies a Caddyfile and never controls the Caddy service.
// The read-only Caddyfile *preview* is allowed; it only formats route JSON.
func TestNoCaddyfileOrServiceControl(t *testing.T) {
	root := filepath.Join("..", "..")
	forbidden := []string{
		"systemctl",
		"systemd",
		`exec.Command("caddy`,
		"caddy reload",
		"caddy adapt",
		"caddyconfig/caddyfile",
		"caddyfile.NewAdapter",
		`os.ReadFile("Caddyfile")`,
		`os.Open("Caddyfile")`,
	}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.Contains(path, "archtest") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			src := string(data)
			for _, bad := range forbidden {
				if strings.Contains(src, bad) {
					t.Errorf("%s contains forbidden %q", path, bad)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestNoProcessExecutionOutsideEditor keeps the backend from spawning Caddy or
// any other system command. The CLI's $EDITOR call is the only allowed exec.
func TestNoProcessExecutionOutsideEditor(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, dir := range []string{"internal/caddy", "internal/core", "internal/web", "internal/state"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), `"os/exec"`) {
				t.Errorf("%s must not execute processes", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
