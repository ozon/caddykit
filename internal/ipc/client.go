// Package ipc is the CLI <-> web-process channel over a 0600 unix socket.
package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"caddykit/internal/state"
)

type APIError struct {
	Status      int
	Code        string
	Message     string
	BlockReason string
	Candidates  []string
}

func (e *APIError) Error() string {
	if e.BlockReason != "" {
		return fmt.Sprintf("%s (%s): %s", e.Message, e.BlockReason, e.Code)
	}
	return e.Message
}

type Client struct {
	hc    *http.Client
	token string
}

// Dial connects to the running web process. It returns an error when no
// usable socket exists.
func Dial(dataDir string) (*Client, error) {
	sock := state.SocketPath(dataDir)
	st, err := os.Stat(sock)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("%s is not a socket", sock)
	}
	token, err := readToken(dataDir)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", sock)
		},
		ResponseHeaderTimeout: 15 * time.Second,
	}
	return &Client{hc: &http.Client{Transport: tr, Timeout: 30 * time.Second}, token: token}, nil
}

func readToken(dataDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, state.FileIPCToken))
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(data)), nil
}

func (c *Client) Ping(ctx context.Context) bool {
	var out map[string]any
	return c.Do(ctx, http.MethodGet, "/api/v1/status", nil, &out) == nil
}

func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("X-Devproxy-Token", c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error       string   `json:"error"`
			Code        string   `json:"code"`
			BlockReason string   `json:"block_reason"`
			Candidates  []string `json:"candidates"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = string(data)
		}
		return &APIError{Status: resp.StatusCode, Code: e.Code, Message: e.Error, BlockReason: e.BlockReason, Candidates: e.Candidates}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

var ErrUnavailable = errors.New("caddykit web process is not reachable")
