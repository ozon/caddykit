package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	ok := map[string]Level{
		"off": Off, "none": Off,
		"error": Error,
		"warn":  Warn, "warning": Warn,
		"info":  Info,
		"debug": Debug, "DEBUG": Debug, " debug ": Debug,
	}
	for in, want := range ok {
		got, err := ParseLevel(in)
		if err != nil {
			t.Fatalf("ParseLevel(%q) error: %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"", "loud", "trace"} {
		if _, err := ParseLevel(bad); err == nil {
			t.Fatalf("ParseLevel(%q) should fail", bad)
		}
	}
}

func TestLevelFiltering(t *testing.T) {
	buf := &bytes.Buffer{}
	l := New(buf, Warn)
	l.Errorf("boom %d", 1)
	l.Warnf("careful")
	l.Infof("chatty")
	l.Debugf("verbose")

	out := buf.String()
	if !strings.Contains(out, "boom 1") || !strings.Contains(out, "careful") {
		t.Fatalf("warn/error lines missing: %q", out)
	}
	if strings.Contains(out, "chatty") || strings.Contains(out, "verbose") {
		t.Fatalf("lines above the level leaked: %q", out)
	}
	if !strings.HasPrefix(out, "caddykit: ") {
		t.Fatalf("missing log prefix: %q", out)
	}
}

func TestFromFlagsWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "caddykit.log")
	l, closer, err := FromFlags("info", path, Off)
	if err != nil {
		t.Fatal(err)
	}
	l.Infof("hello %s", "file")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello file") {
		t.Fatalf("log file misses the line: %q", data)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("log file mode = %o, want 600", st.Mode().Perm())
	}
}

func TestFromFlagsOffCreatesNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "off.log")
	l, closer, err := FromFlags("off", path, Info)
	if err != nil {
		t.Fatal(err)
	}
	l.Errorf("never")
	_ = closer.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("off level created a log file: %v", err)
	}
}

func TestFromFlagsDefaultLevel(t *testing.T) {
	l, _, err := FromFlags("", "", Debug)
	if err != nil {
		t.Fatal(err)
	}
	if l.Level() != Debug {
		t.Fatalf("level = %v, want debug", l.Level())
	}
}

func TestNilLoggerIsSafe(t *testing.T) {
	var l *Logger
	l.Infof("no panic")
	l.Errorf("no panic")
	if l.Enabled(Info) {
		t.Fatal("nil logger must be disabled")
	}
}
