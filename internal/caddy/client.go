// Package caddy is a minimal Caddy admin API client. All mutations use the
// optimistic concurrency protocol from spec §4.1 (Etag / If-Match).
package caddy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"caddykit/internal/config"
)

var (
	ErrPreconditionFailed = errors.New("caddy: precondition failed")
	ErrNotFound           = errors.New("caddy: not found")
)

// StatusError is returned for unexpected non-2xx responses.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("caddy admin API: HTTP %d: %s", e.Code, strings.TrimSpace(e.Body))
}

type Client struct {
	base    string
	network string
	addr    string
	hc      *http.Client
}

// New creates a client for an http(s) URL or the caddy-style unix//path form.
func New(raw string) (*Client, error) {
	sock, _, err := config.ParseAdmin(raw)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
	c := &Client{hc: &http.Client{Transport: tr, Timeout: 15 * time.Second}}
	if sock != "" {
		c.network, c.addr, c.base = "unix", sock, "http://unix"
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", sock)
		}
	} else {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		c.base = strings.TrimRight(u.String(), "/")
	}
	return c, nil
}

func (c *Client) endpoint(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.base + path
}

// Do performs one admin request. ifMatch is sent as If-Match when non-empty.
func (c *Client) Do(ctx context.Context, method, path string, body []byte, ifMatch string) ([]byte, http.Header, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), r)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, resp.Header, err
	}
	// Caddy < 2.8 sends the Etag as an HTTP trailer instead of a response
	// header. Trailers are only populated after the body has been read.
	if resp.Header.Get("Etag") == "" {
		if v := resp.Trailer.Get("Etag"); v != "" {
			resp.Header.Set("Etag", v)
		}
	}
	switch {
	case resp.StatusCode == http.StatusPreconditionFailed:
		return data, resp.Header, fmt.Errorf("%w: %s", ErrPreconditionFailed, strings.TrimSpace(string(data)))
	case resp.StatusCode == http.StatusNotFound:
		return data, resp.Header, fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(string(data)))
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return data, resp.Header, &StatusError{Code: resp.StatusCode, Body: string(data)}
	}
	return data, resp.Header, nil
}

func (c *Client) Get(ctx context.Context, path string) ([]byte, http.Header, error) {
	return c.Do(ctx, http.MethodGet, path, nil, "")
}

func (c *Client) GetWithETag(ctx context.Context, path string) ([]byte, string, error) {
	data, hdr, err := c.Get(ctx, path)
	if err != nil {
		return nil, "", err
	}
	return data, hdr.Get("Etag"), nil
}

func (c *Client) Put(ctx context.Context, path string, body []byte, ifMatch string) error {
	_, _, err := c.Do(ctx, http.MethodPut, path, body, ifMatch)
	return err
}

func (c *Client) Post(ctx context.Context, path string, body []byte, ifMatch string) error {
	_, _, err := c.Do(ctx, http.MethodPost, path, body, ifMatch)
	return err
}

// Patch replaces an existing config value. Caddy's PUT refuses to replace an
// existing key, so existing route arrays must be written with PATCH.
func (c *Client) Patch(ctx context.Context, path string, body []byte, ifMatch string) error {
	_, _, err := c.Do(ctx, http.MethodPatch, path, body, ifMatch)
	return err
}

func (c *Client) Delete(ctx context.Context, path string, ifMatch string) error {
	_, _, err := c.Do(ctx, http.MethodDelete, path, nil, ifMatch)
	return err
}

// Check verifies that Caddy is reachable and sends the Etag header
// (required Caddy >= 2.8.0, spec §1).
func (c *Client) Check(ctx context.Context) error {
	_, hdr, err := c.Get(ctx, "/config/")
	if err != nil {
		return err
	}
	if hdr.Get("Etag") == "" {
		return errors.New("caddy admin API does not send an Etag (header or trailer); check the admin API and Caddy version")
	}
	return nil
}

// ServerPath returns the escaped config path of one HTTP server.
func ServerPath(server string) string {
	return "/config/apps/http/servers/" + url.PathEscape(server)
}

func RoutesPath(server string) string {
	return ServerPath(server) + "/routes"
}

// IDPath is only usable for IDs without a slash. The IDs mandated by the spec
// contain slashes and are resolved through the full route list instead.
func IDPath(id string) string {
	return "/id/" + url.PathEscape(id)
}
