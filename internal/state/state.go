// Package state persists the desired state, the single undo step and the
// operation journal with 0600 files and atomic writes (spec §3).
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"devproxy/internal/model"
)

const SchemaVersion = 1

const (
	FileState     = "state.json"
	FileUndo      = "undo.json"
	FileJournal   = "opjournal.json"
	FileInstance  = "devproxy.lock"
	FileStateLock = "state.lock"
	FileIPCToken  = "ipc.token"
)

type Anchor struct {
	Index int `json:"index"`
}

type TemplateRef struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type Entry struct {
	ID        string       `json:"id"`
	Snapshot  model.Route  `json:"snapshot"`
	Anchor    Anchor       `json:"anchor"`
	AdoptedAt time.Time    `json:"adopted_at"`
	Source    string       `json:"source"` // create | adopt | template
	Template  *TemplateRef `json:"template,omitempty"`
}

type State struct {
	SchemaVersion int              `json:"schema_version"`
	Server        string           `json:"server"`
	Routes        map[string]Entry `json:"routes"`
}

func NewState(server string) *State {
	return &State{SchemaVersion: SchemaVersion, Server: server, Routes: map[string]Entry{}}
}

// Image is one side of an operation (Caddy route and/or local entry).
type Image struct {
	Route model.Route `json:"route,omitempty"`
	Entry *Entry      `json:"entry,omitempty"`
	Index int         `json:"index,omitempty"`
}

type Undo struct {
	OpID        string    `json:"op_id"`
	Kind        string    `json:"kind"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	Before      *Image    `json:"before"`
	After       *Image    `json:"after"`
}

const (
	PhasePrepared     = "prepared"
	PhaseCaddyWritten = "caddy_written"
	PhaseUnclear      = "unclear"
)

type Journal struct {
	OpID      string    `json:"op_id"`
	Kind      string    `json:"kind"`
	RouteID   string    `json:"route_id"`
	Phase     string    `json:"phase"`
	Old       *Image    `json:"old,omitempty"`
	New       *Image    `json:"new,omitempty"`
	Undo      *Undo     `json:"undo,omitempty"`
	TouchUndo bool      `json:"touch_undo"`
	ClearUndo bool      `json:"clear_undo,omitempty"`
	LocalOnly bool      `json:"local_only,omitempty"`
	BaseETag  string    `json:"base_etag,omitempty"`
	StartedAt time.Time `json:"started_at"`
	Error     string    `json:"error,omitempty"`
}

type Store struct {
	Dir  string
	held *FileLock
}

func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("state: empty data directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) Path(name string) string { return filepath.Join(s.Dir, name) }

func (s *Store) LoadState() (*State, error) {
	data, err := s.read(FileState)
	if errors.Is(err, os.ErrNotExist) {
		return NewState(""), nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("state.json: %w", err)
	}
	if st.SchemaVersion == 0 {
		st.SchemaVersion = 1
	}
	if st.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("state.json schema_version %d is newer than supported %d", st.SchemaVersion, SchemaVersion)
	}
	if st.Routes == nil {
		st.Routes = map[string]Entry{}
	}
	return &st, nil
}

func (s *Store) SaveState(st *State) error {
	st.SchemaVersion = SchemaVersion
	return s.writeJSON(FileState, st)
}

func (s *Store) LoadUndo() (*Undo, error) {
	data, err := s.read(FileUndo)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var u Undo
	if err := json.Unmarshal(data, &u); err != nil {
		return nil, fmt.Errorf("undo.json: %w", err)
	}
	return &u, nil
}

func (s *Store) SaveUndo(u *Undo) error { return s.writeJSON(FileUndo, u) }

func (s *Store) ClearUndo() error {
	err := os.Remove(s.Path(FileUndo))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) LoadJournal() (*Journal, error) {
	data, err := s.read(FileJournal)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j Journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("opjournal.json: %w", err)
	}
	return &j, nil
}

func (s *Store) SaveJournal(j *Journal) error { return s.writeJSON(FileJournal, j) }

func (s *Store) ClearJournal() error {
	err := os.Remove(s.Path(FileJournal))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) read(name string) ([]byte, error) {
	p := s.Path(name)
	_ = os.Chmod(p, 0o600)
	return os.ReadFile(p)
}

func (s *Store) writeJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.Path(name), data)
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Token returns the IPC token, creating it on first use.
func (s *Store) Token() (string, error) {
	p := s.Path(FileIPCToken)
	data, err := os.ReadFile(p)
	if err == nil {
		tok := strings.TrimSpace(string(data))
		if len(tok) >= 32 {
			return tok, nil
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := writeFileAtomic(p, []byte(tok+"\n")); err != nil {
		return "", err
	}
	return tok, nil
}

// SocketPath returns $XDG_RUNTIME_DIR/devproxy.sock or a socket inside the
// data directory as fallback.
func SocketPath(dataDir string) string {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		if st, err := os.Stat(rt); err == nil && st.IsDir() {
			return filepath.Join(rt, "devproxy.sock")
		}
	}
	return filepath.Join(dataDir, "devproxy.sock")
}
