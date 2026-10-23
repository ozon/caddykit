package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"caddykit/internal/model"
)

func TestStoreRoundtripAndModes(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := NewState("srv0")
	id := model.NewID()
	st.Routes[id] = Entry{ID: id, Snapshot: model.Spec{Host: "a.example.com", Upstream: "127.0.0.1:1"}.Build(), Source: "create", AdoptedAt: time.Now()}
	if err := s.SaveState(st); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveUndo(&Undo{OpID: "op", Kind: "create", Description: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJournal(&Journal{OpID: "op", Kind: "create", RouteID: id, Phase: PhasePrepared}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{FileState, FileUndo, FileJournal} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", name, info.Mode().Perm())
		}
	}
	got, err := s.LoadState()
	if err != nil || got.Routes[id].ID != id {
		t.Fatalf("state roundtrip: %+v %v", got, err)
	}
	u, _ := s.LoadUndo()
	if u == nil || u.Kind != "create" {
		t.Fatalf("undo roundtrip: %+v", u)
	}
	j, _ := s.LoadJournal()
	if j == nil || j.Phase != PhasePrepared {
		t.Fatalf("journal roundtrip: %+v", j)
	}
	if err := s.ClearJournal(); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.LoadJournal(); j != nil {
		t.Fatal("journal not cleared")
	}
}

func TestNewerSchemaRejected(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if err := os.WriteFile(filepath.Join(dir, FileState), []byte(`{"schema_version":99,"routes":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadState(); err == nil {
		t.Fatal("newer schema must be rejected")
	}
}

func TestInstanceLock(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	l1, err := TryLock(s.Path(FileInstance))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(s.Path(FileInstance)); !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	if err := l1.Close(); err != nil {
		t.Fatal(err)
	}
	l2, err := TryLock(s.Path(FileInstance))
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	_ = l2.Close()
}

func TestStateLockSerializes(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	got := make(chan struct{})
	go func() {
		_ = s.WithStateLock(func() error {
			time.Sleep(50 * time.Millisecond)
			close(got)
			return nil
		})
	}()
	time.Sleep(10 * time.Millisecond)
	if err := s.WithStateLock(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	default:
		t.Fatal("state lock did not serialize")
	}
}

func TestTokenCreated0600(t *testing.T) {
	s, _ := Open(t.TempDir())
	t1, err := s.Token()
	if err != nil || len(t1) < 32 {
		t.Fatalf("token: %q %v", t1, err)
	}
	t2, _ := s.Token()
	if t1 != t2 {
		t.Fatal("token not stable")
	}
	info, err := os.Stat(s.Path(FileIPCToken))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode %v", info.Mode().Perm())
	}
}
