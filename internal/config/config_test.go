package config

import (
	"flag"
	"testing"
	"time"
)

func TestEnvThenFlagPrecedence(t *testing.T) {
	t.Setenv("CADDYKIT_CADDY_ADMIN", "http://127.0.0.1:2020")
	t.Setenv("CADDYKIT_RECONCILE_INTERVAL", "12s")
	c := FromEnv()
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
	c := FromEnv()
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
	c := FromEnv()
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
	c := FromEnv()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	c.RegisterCommon(fs)
	if err := fs.Parse([]string{"--log-level", "error", "--log-file", "/tmp/x.log"}); err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "error" || c.LogFile != "/tmp/x.log" {
		t.Fatalf("log flags not applied: %+v", c)
	}
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
