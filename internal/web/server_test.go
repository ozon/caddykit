package web

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newLoggedServer(t *testing.T) (*Server, *bytes.Buffer) {
	t.Helper()
	auth, err := NewAuth()
	if err != nil {
		t.Fatal(err)
	}
	buf := &bytes.Buffer{}
	return &Server{opts: Options{Logger: log.New(buf, "", 0)}, auth: auth}, buf
}

func TestRequestLogRecordsStatusAndDropsQuery(t *testing.T) {
	s, buf := newLoggedServer(t)
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nope?token=topsecret", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	line := buf.String()
	if !strings.Contains(line, "GET /api/v1/nope 404") {
		t.Fatalf("request log = %q, want method, path and status", line)
	}
	if strings.Contains(line, "topsecret") || strings.Contains(line, "?") {
		t.Fatalf("request log leaked the query string: %q", line)
	}
}

func TestRequestLogIgnoresNonAPIPaths(t *testing.T) {
	s, buf := newLoggedServer(t)
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if buf.Len() != 0 {
		t.Fatalf("non-API request was logged: %q", buf.String())
	}
}

func TestLoginFailuresAreLoggedWithoutPassword(t *testing.T) {
	s, buf := newLoggedServer(t)
	h := s.handler()

	statuses := make([]int, 0, 5)
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		body := strings.NewReader(`{"password":"hunter2"}`)
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/login", body))
		statuses = append(statuses, rec.Code)
	}
	if got := statuses[len(statuses)-1]; got != http.StatusTooManyRequests {
		t.Fatalf("statuses = %v, want the fifth attempt rate limited", statuses)
	}

	out := buf.String()
	if !strings.Contains(out, "login failed for") {
		t.Fatalf("login failures were not logged: %q", out)
	}
	if !strings.Contains(out, "login rate-limited for") {
		t.Fatalf("rate limiting was not logged: %q", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Fatalf("log leaked the submitted password: %q", out)
	}
}
