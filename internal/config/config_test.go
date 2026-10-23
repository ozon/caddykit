package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnvThenFlagPrecedence(t *testing.T) {
	t.Setenv("CADDYKIT_CADDY_ADMIN", "http://127.0.0.1:2020")
	t.Setenv("CADDYKIT_RECONCILE_INTERVAL", "12s")
	c := envConfig(t)
	if c.CaddyAdmin != "http://127.0.0.1:2020" || c.ReconcileInterval != 12*time.Second {
		t.Fatalf("env not applied: %+v", c)
	}
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	c.Register(fs)
	if err := fs.Parse([]string{"--caddy-admin", "http://127.0.0.1:2021"}); err != nil {
		t.Fatal(err)
	}
	if c.CaddyAdmin != "http://127.0.0.1:2021" {
		t.Fatalf("flag did not win: %s", c.CaddyAdmin)
	}
}

func TestRemoteAdminGuard(t *testing.T) {
	c := envConfig(t)
	c.CaddyAdmin = "http://caddy.example:2019"
	if err := c.Validate(false); err == nil {
		t.Fatal("remote plaintext admin must be rejected")
	}
	c.AllowRemoteAdmin = true
	if err := c.Validate(false); err == nil {
		t.Fatal("remote plaintext admin must stay rejected even with the flag")
	}
	c.CaddyAdmin = "https://caddy.example:2019"
	if err := c.Validate(false); err != nil {
		t.Fatalf("https remote should be allowed: %v", err)
	}
	c.CaddyAdmin = "unix//run/caddy/admin.sock"
	if err := c.Validate(false); err != nil {
		t.Fatalf("unix admin should be allowed: %v", err)
	}
}

func TestLogLevelValidation(t *testing.T) {
	t.Setenv("CADDYKIT_LOG_LEVEL", "debug")
	c := envConfig(t)
	if c.LogLevel != "debug" {
		t.Fatalf("env log level not applied: %q", c.LogLevel)
	}
	if err := c.Validate(false); err != nil {
		t.Fatalf("debug must be accepted: %v", err)
	}
	c.LogLevel = "loud"
	if err := c.Validate(false); err == nil {
		t.Fatal("unknown log level must be rejected")
	}
}

func TestLogFlagsRegistered(t *testing.T) {
	c := envConfig(t)
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	c.RegisterCommon(fs)
	if err := fs.Parse([]string{"--log-level", "error", "--log-file", "/tmp/x.log"}); err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "error" || c.LogFile != "/tmp/x.log" {
		t.Fatalf("log flags not applied: %+v", c)
	}
}

func TestFileThenEnvThenFlagPrecedence(t *testing.T) {
	writeConfig(t, `
domain: file.example.com
caddy_admin: http://127.0.0.1:2030
reconcile_interval: 15s
server: filesrv
log_level: warn
`)
	t.Setenv("CADDYKIT_CADDY_ADMIN", "http://127.0.0.1:2040")
	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.Domain != "file.example.com" || c.Server != "filesrv" || c.LogLevel != "warn" {
		t.Fatalf("file values not applied: %+v", c)
	}
	if c.ReconcileInterval != 15*time.Second {
		t.Fatalf("file interval not applied: %v", c.ReconcileInterval)
	}
	if c.CaddyAdmin != "http://127.0.0.1:2040" {
		t.Fatalf("env must beat the file: %s", c.CaddyAdmin)
	}
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	c.Register(fs)
	if err := fs.Parse([]string{"--caddy-admin", "http://127.0.0.1:2050"}); err != nil {
		t.Fatal(err)
	}
	if c.CaddyAdmin != "http://127.0.0.1:2050" {
		t.Fatalf("flag must beat env: %s", c.CaddyAdmin)
	}
	if err := c.Validate(true); err != nil {
		t.Fatalf("file domain must pass web validation: %v", err)
	}
}

func TestConfigFileRejectsUnknownKeys(t *testing.T) {
	for _, key := range []string{"allow_remote_admin", "allow_insecure", "domian"} {
		t.Run(key, func(t *testing.T) {
			writeConfig(t, key+": true\n")
			_, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("unknown key %q must be rejected, got %v", key, err)
			}
		})
	}
}

func TestConfigFileRejectsBadInterval(t *testing.T) {
	writeConfig(t, "reconcile_interval: nonsense\n")
	_, err := FromEnv()
	if err == nil || !strings.Contains(err.Error(), "reconcile_interval") {
		t.Fatalf("bad interval must be rejected, got %v", err)
	}
}

func TestEmptyConfigFileUsesDefaults(t *testing.T) {
	writeConfig(t, "")
	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.CaddyAdmin != DefaultCaddyAdmin || c.ReconcileInterval != DefaultReconcileInterval {
		t.Fatalf("empty file must keep defaults: %+v", c)
	}
}

func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	p := filepath.Join(dir, "caddykit", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func envConfig(t *testing.T) Config {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	return c
}

func TestDomainValidation(t *testing.T) {
	for _, ok := range []string{"a.example.com", "dev-proxy.example.org"} {
		if err := ValidateDomain(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "localhost", "-a.example", "a..b", "a_b.example", "ä.example"} {
		if err := ValidateDomain(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
