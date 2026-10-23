package caddy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEtagRoundtripAndPrecondition(t *testing.T) {
	const etag = `"/config/apps/http/servers/srv0 abc123"`
	var gotIfMatch string
	mux := http.NewServeMux()
	mux.HandleFunc("/config/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Etag", `"/config/ abc123"`)
			_, _ = w.Write([]byte(`{}`))
			return
		}
	})
	mux.HandleFunc("/config/apps/http/servers/srv0", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", etag)
		_, _ = w.Write([]byte(`{"listen":[":80"],"routes":[]}`))
	})
	mux.HandleFunc("/config/apps/http/servers/srv0/routes", func(w http.ResponseWriter, r *http.Request) {
		gotIfMatch = r.Header.Get("If-Match")
		if gotIfMatch != etag {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	_, got, err := c.GetWithETag(ctx, ServerPath("srv0"))
	if err != nil || got != etag {
		t.Fatalf("etag = %q, err=%v", got, err)
	}
	if err := c.Put(ctx, RoutesPath("srv0"), []byte(`[]`), got); err != nil {
		t.Fatalf("put: %v", err)
	}
	if gotIfMatch != etag {
		t.Fatalf("If-Match not forwarded: %q", gotIfMatch)
	}
	err = c.Put(ctx, RoutesPath("srv0"), []byte(`[]`), `"/config/apps/http/servers/srv0 stale"`)
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("expected ErrPreconditionFailed, got %v", err)
	}
}

func TestCheckRequiresEtag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Check(context.Background()); err == nil {
		t.Fatal("expected error for missing Etag")
	}
}

func TestEtagTrailer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "Etag")
		_, _ = w.Write([]byte(`{}`))
		w.Header().Set("Etag", `"/config/ abc123"`)
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Check(ctx); err != nil {
		t.Fatalf("trailer Etag not accepted: %v", err)
	}
	body, etag, err := c.GetWithETag(ctx, "/config/")
	if err != nil || string(body) != `{}` || etag != `"/config/ abc123"` {
		t.Fatalf("body=%q etag=%q err=%v", body, etag, err)
	}
}
