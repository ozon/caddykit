// Package config resolves caddykit configuration with the precedence
// CLI argument > environment variable > config file > default (spec §1.1).
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"caddykit/internal/logging"
)

const (
	DefaultCaddyAdmin        = "http://localhost:2019"
	DefaultReconcileInterval = 10 * time.Second
	MinReconcileInterval     = 2 * time.Second
	MaxReconcileInterval     = 5 * time.Minute
)

type Config struct {
	CaddyAdmin        string
	Domain            string
	DataDir           string
	TemplateDir       string
	ReconcileInterval time.Duration
	Server            string
	AllowRemoteAdmin  bool
	AllowInsecure     bool
	LogLevel          string
	LogFile           string
}

// fileConfig is the YAML schema of $XDG_CONFIG_HOME/caddykit/config.yaml.
// Security-relevant flags (allow_remote_admin, allow_insecure) are absent on
// purpose: unknown keys are rejected, so they cannot be set from the file.
type fileConfig struct {
	Domain            string `yaml:"domain"`
	CaddyAdmin        string `yaml:"caddy_admin"`
	DataDir           string `yaml:"data_dir"`
	TemplateDir       string `yaml:"template_dir"`
	ReconcileInterval string `yaml:"reconcile_interval"`
	Server            string `yaml:"server"`
	LogLevel          string `yaml:"log_level"`
	LogFile           string `yaml:"log_file"`
}

// Defaults returns the built-in configuration.
func Defaults() Config {
	return Config{
		CaddyAdmin:        DefaultCaddyAdmin,
		DataDir:           filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "caddykit"),
		TemplateDir:       filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "caddykit", "templates"),
		ReconcileInterval: DefaultReconcileInterval,
	}
}

// ConfigPath is where FromEnv looks for the settings file.
func configPath() string {
	return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "caddykit", "config.yaml")
}

// FromEnv builds the configuration from defaults, the settings file, and the
// environment, in that order.
func FromEnv() (Config, error) {
	c := Defaults()
	if err := c.applyFile(configPath()); err != nil {
		return Config{}, err
	}
	c.applyEnv()
	return c, nil
}

func (c *Config) applyFile(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var fc fileConfig
	if err := dec.Decode(&fc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // empty file
		}
		return fmt.Errorf("config file %s: %w", path, err)
	}
	if fc.Domain != "" {
		c.Domain = fc.Domain
	}
	if fc.CaddyAdmin != "" {
		c.CaddyAdmin = fc.CaddyAdmin
	}
	if fc.DataDir != "" {
		c.DataDir = fc.DataDir
	}
	if fc.TemplateDir != "" {
		c.TemplateDir = fc.TemplateDir
	}
	if fc.Server != "" {
		c.Server = fc.Server
	}
	if fc.LogLevel != "" {
		c.LogLevel = fc.LogLevel
	}
	if fc.LogFile != "" {
		c.LogFile = fc.LogFile
	}
	if fc.ReconcileInterval != "" {
		d, err := time.ParseDuration(fc.ReconcileInterval)
		if err != nil {
			return fmt.Errorf("config file %s: reconcile_interval: %w", path, err)
		}
		c.ReconcileInterval = d
	}
	return nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("CADDYKIT_CADDY_ADMIN"); v != "" {
		c.CaddyAdmin = v
	}
	if v := os.Getenv("CADDYKIT_DOMAIN"); v != "" {
		c.Domain = v
	}
	if v := os.Getenv("CADDYKIT_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("CADDYKIT_TEMPLATE_DIR"); v != "" {
		c.TemplateDir = v
	}
	if v := os.Getenv("CADDYKIT_SERVER"); v != "" {
		c.Server = v
	}
	if v := os.Getenv("CADDYKIT_LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
	if v := os.Getenv("CADDYKIT_LOG_FILE"); v != "" {
		c.LogFile = v
	}
	if v := os.Getenv("CADDYKIT_RECONCILE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.ReconcileInterval = d
		}
	}
}

func xdg(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fallback
	}
	return filepath.Join(home, fallback)
}

// Register adds all configuration flags to fs. Defaults are already resolved
// from file and environment by FromEnv.
func (c *Config) Register(fs *flag.FlagSet) {
	c.RegisterCommon(fs)
	fs.StringVar(&c.Domain, "domain", c.Domain, "public domain of the web UI")
}

// RegisterCommon adds every flag except --domain. route commands use --domain
// for the route host instead.
func (c *Config) RegisterCommon(fs *flag.FlagSet) {
	fs.StringVar(&c.CaddyAdmin, "caddy-admin", c.CaddyAdmin, "Caddy admin API URL (or unix//path)")
	fs.StringVar(&c.DataDir, "data-dir", c.DataDir, "data directory")
	fs.StringVar(&c.TemplateDir, "template-dir", c.TemplateDir, "template directory")
	fs.DurationVar(&c.ReconcileInterval, "reconcile-interval", c.ReconcileInterval, "reconcile interval (2s..5m)")
	fs.StringVar(&c.Server, "server", c.Server, "Caddy HTTP server name (autodetect when empty)")
	fs.BoolVar(&c.AllowRemoteAdmin, "allow-remote-admin", false, "allow a non-local Caddy admin endpoint (HTTPS or unix only)")
	fs.BoolVar(&c.AllowInsecure, "allow-insecure", false, "allow insecure upstream TLS verification")
	fs.StringVar(&c.LogLevel, "log-level", c.LogLevel, "log level: off, error, warn, info or debug")
	fs.StringVar(&c.LogFile, "log-file", c.LogFile, "write logs to this file instead of stderr")
}

// Validate checks the configuration. requireDomain is true for the web process.
func (c *Config) Validate(requireDomain bool) error {
	if requireDomain && strings.TrimSpace(c.Domain) == "" {
		return fmt.Errorf("--domain is required for the web process")
	}
	if c.Domain != "" && requireDomain {
		if err := ValidateDomain(c.Domain); err != nil {
			return fmt.Errorf("invalid --domain: %w", err)
		}
	}
	if c.DataDir == "" {
		return errors.New("--data-dir must not be empty")
	}
	if c.LogLevel != "" {
		if _, err := logging.ParseLevel(c.LogLevel); err != nil {
			return fmt.Errorf("--log-level: %w", err)
		}
	}
	// The interval is only used by the web process; a web-only setting must
	// not make route commands fail.
	if requireDomain && (c.ReconcileInterval < MinReconcileInterval || c.ReconcileInterval > MaxReconcileInterval) {
		return fmt.Errorf("--reconcile-interval must be between %s and %s", MinReconcileInterval, MaxReconcileInterval)
	}
	_, local, err := ParseAdmin(c.CaddyAdmin)
	if err != nil {
		return err
	}
	if !local {
		if !c.AllowRemoteAdmin {
			return errors.New("remote Caddy admin endpoint requires --allow-remote-admin")
		}
		if !strings.HasPrefix(strings.ToLower(c.CaddyAdmin), "https://") && !strings.HasPrefix(c.CaddyAdmin, "unix/") {
			return errors.New("remote Caddy admin endpoint must use HTTPS or a unix socket")
		}
	}
	return nil
}

// ParseAdmin reports whether the admin endpoint is local. It accepts http(s)
// URLs and the caddy-style unix//path form.
func ParseAdmin(raw string) (sock string, local bool, err error) {
	if strings.HasPrefix(raw, "unix/") {
		p := strings.TrimPrefix(raw, "unix/")
		if !filepath.IsAbs(p) {
			return "", false, fmt.Errorf("unix admin socket must be an absolute path: %s", raw)
		}
		return p, true, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("invalid --caddy-admin: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false, fmt.Errorf("invalid --caddy-admin scheme %q", u.Scheme)
	}
	host := u.Hostname()
	local = host == "localhost" || host == "127.0.0.1" || host == "::1" || host == ""
	return "", local, nil
}

// ValidateDomain performs a conservative ASCII hostname check.
// shortcut: IDNA/punycode input is not converted; non-ASCII domains are
// rejected. Upgrade with golang.org/x/net/idna if international domains matter.
func ValidateDomain(d string) error {
	d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
	if d == "" || len(d) > 253 {
		return errors.New("empty or too long")
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return errors.New("needs at least two labels")
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 {
			return errors.New("invalid label")
		}
		if l[0] == '-' || l[len(l)-1] == '-' {
			return errors.New("label starts or ends with '-'")
		}
		for _, r := range l {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("invalid character %q", r)
			}
		}
	}
	return nil
}

// EnsureDataDir creates the data directory with mode 0700.
func EnsureDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}
