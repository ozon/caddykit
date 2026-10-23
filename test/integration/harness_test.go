//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"testing"
	"time"
)

var (
	buildOnce sync.Once
	binary    string
	buildErr  error

	usedPorts   = map[int]bool{}
	usedPortsMu sync.Mutex
)

func freePort(t *testing.T) int {
	t.Helper()
	usedPortsMu.Lock()
	defer usedPortsMu.Unlock()
	for i := 0; i < 100; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if !usedPorts[p] {
			usedPorts[p] = true
			return p
		}
	}
	t.Fatal("no free port")
	return 0
}

func devproxyBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "devproxy-it-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		binary = filepath.Join(dir, "devproxy")
		cmd := exec.Command("go", "build", "-o", binary, "./cmd/devproxy")
		cmd.Dir = repoRoot()
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binary
}

func repoRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

type caddyBox struct {
	name    string
	admin   string
	http    int
	image   string
	cleanup func()
}

func startCaddy(t *testing.T) *caddyBox {
	t.Helper()
	image := os.Getenv("CADDY_IMAGE")
	if image == "" {
		image = "caddy:2.10"
	}
	adminPort := freePort(t)
	httpPort := freePort(t)
	seed := fmt.Sprintf(`{
  "admin": {"listen": "127.0.0.1:%d"},
  "apps": {"http": {"servers": {"srv0": {
    "listen": [":%d"],
    "automatic_https": {"disable": true},
    "routes": []
  }}}}
}`, adminPort, httpPort)
	seedFile := filepath.Join(t.TempDir(), "seed.json")
	if err := os.WriteFile(seedFile, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("devproxy-it-%d-%d", os.Getpid(), adminPort)
	cmd := exec.Command("docker", "run", "-d", "--name", name, "--network", "host",
		"-v", seedFile+":/seed.json:ro", image, "caddy", "run", "--config", "/seed.json")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	box := &caddyBox{name: name, admin: fmt.Sprintf("http://127.0.0.1:%d", adminPort), http: httpPort, image: image}
	box.cleanup = func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	}
	t.Cleanup(box.cleanup)
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(box.admin + "/config/")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return box
			}
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Fatalf("caddy did not start: %v\n%s", err, logs)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (c *caddyBox) routes(t *testing.T) []map[string]any {
	t.Helper()
	resp, err := http.Get(c.admin + "/config/apps/http/servers/srv0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var obj struct {
		Routes []map[string]any `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		t.Fatal(err)
	}
	return obj.Routes
}

func (c *caddyBox) setRoutes(t *testing.T, routes []map[string]any) {
	t.Helper()
	resp, err := http.Get(c.admin + "/config/apps/http/servers/srv0")
	if err != nil {
		t.Fatal(err)
	}
	etag := resp.Header.Get("Etag")
	resp.Body.Close()
	body, _ := json.Marshal(routes)
	req, _ := http.NewRequest(http.MethodPatch, c.admin+"/config/apps/http/servers/srv0/routes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", etag)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("set routes: %d %s", resp.StatusCode, b)
	}
}

func (c *caddyBox) findRoute(routes []map[string]any, id string) map[string]any {
	for _, r := range routes {
		if r["@id"] == id {
			return r
		}
	}
	return nil
}

func (c *caddyBox) selfRoute(t *testing.T) map[string]any {
	t.Helper()
	for _, r := range c.routes(t) {
		if r["@id"] == "devproxy/self" {
			return r
		}
	}
	return nil
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type webProc struct {
	cmd      *exec.Cmd
	stdout   *syncBuf
	stderr   *syncBuf
	base     string
	password string
	runDir   string
	done     chan error
}

var backendRE = regexp.MustCompile(`backend listening on (127\.0\.0\.1:\d+)`)
var passwordRE = regexp.MustCompile(`Startpasswort: ([A-Z2-7]+)`)

func startWeb(t *testing.T, box *caddyBox, domain, dataDir string) *webProc {
	t.Helper()
	runDir := filepath.Join(t.TempDir(), "run")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	w := &webProc{stdout: &syncBuf{}, stderr: &syncBuf{}, runDir: runDir, done: make(chan error, 1)}
	args := []string{"web", "--domain", domain, "--caddy-admin", box.admin, "--server", "srv0",
		"--data-dir", dataDir, "--reconcile-interval", "2s"}
	w.cmd = exec.Command(devproxyBinary(t), args...)
	w.cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runDir)
	w.cmd.Stdout = w.stdout
	w.cmd.Stderr = w.stderr
	if err := w.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { w.done <- w.cmd.Wait() }()

	deadline := time.Now().Add(20 * time.Second)
	for {
		out := w.stderr.String()
		if m := backendRE.FindStringSubmatch(out); m != nil {
			w.base = "http://" + m[1]
			if pm := passwordRE.FindStringSubmatch(w.stdout.String()); pm != nil {
				w.password = pm[1]
				if w.password != "" {
					return w
				}
			}
		}
		select {
		case err := <-w.done:
			t.Fatalf("web exited early: %v\nstdout:\n%s\nstderr:\n%s", err, w.stdout.String(), w.stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			signalWeb(w, syscall.SIGKILL)
			t.Fatalf("web did not become ready\nstdout:\n%s\nstderr:\n%s", w.stdout.String(), w.stderr.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// cli runs the same binary against the running web process over IPC.
func (w *webProc) cli(t *testing.T, dataDir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(devproxyBinary(t), args...)
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+w.runDir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func signalWeb(w *webProc, sig os.Signal) {
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		return
	}
	_ = w.cmd.Process.Signal(sig)
}

func stopWeb(t *testing.T, w *webProc) {
	t.Helper()
	signalWeb(w, syscall.SIGTERM)
	select {
	case <-w.done:
	case <-time.After(15 * time.Second):
		signalWeb(w, syscall.SIGKILL)
		t.Fatal("web did not stop on SIGTERM")
	}
}

type apiClient struct {
	t       *testing.T
	base    string
	session string
	csrf    string
}

func (a *apiClient) request(method, path string, body any, withCSRF bool) (int, []byte, http.Header) {
	a.t.Helper()
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, a.base+path, r)
	if err != nil {
		a.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.session != "" {
		req.Header.Set("Cookie", "devproxy_session="+a.session)
	}
	if withCSRF && a.csrf != "" {
		req.Header.Set("X-CSRF-Token", a.csrf)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, resp.Header
}

func (a *apiClient) login(password string) {
	a.t.Helper()
	status, body, hdr := a.request("POST", "/api/v1/login", map[string]string{"password": password}, false)
	if status != 200 {
		a.t.Fatalf("login failed: %d %s", status, body)
	}
	for _, c := range (&http.Response{Header: hdr}).Cookies() {
		if c.Name == "devproxy_session" {
			if !c.Secure || !c.HttpOnly {
				a.t.Fatalf("session cookie is not Secure+HttpOnly: %+v", c)
			}
			if c.SameSite != http.SameSiteStrictMode {
				a.t.Fatalf("session cookie SameSite=%v", c.SameSite)
			}
			a.session = c.Value
		}
	}
	if a.session == "" {
		a.t.Fatal("no session cookie")
	}
	var out struct {
		CSRF string `json:"csrf"`
	}
	_ = json.Unmarshal(body, &out)
	if out.CSRF == "" {
		a.t.Fatal("no CSRF token")
	}
	a.csrf = out.CSRF
}

func (a *apiClient) json(method, path string, in, out any) int {
	a.t.Helper()
	status, body, _ := a.request(method, path, in, method != "GET")
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			a.t.Fatalf("%s %s: decode %v (%s)", method, path, err, body)
		}
	}
	return status
}

func (a *apiClient) preview(kind string, fields map[string]any) map[string]any {
	a.t.Helper()
	in := map[string]any{"kind": kind}
	for k, v := range fields {
		in[k] = v
	}
	var p map[string]any
	if s := a.json("POST", "/api/v1/routes/preview", in, &p); s != 200 {
		a.t.Fatalf("preview %s: %d %+v", kind, s, p)
	}
	return p
}

func (a *apiClient) apply(p map[string]any) map[string]any {
	a.t.Helper()
	var res map[string]any
	if s := a.json("POST", "/api/v1/routes/apply", p, &res); s != 200 {
		a.t.Fatalf("apply: %d %+v", s, res)
	}
	return res
}

func waitFor(t *testing.T, what string, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}
