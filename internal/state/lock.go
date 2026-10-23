package state

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var ErrLocked = errors.New("state: lock is held by another process")

// FileLock is an flock(2) based lock. The lock is released on Close or when
// the process exits.
type FileLock struct {
	f    *os.File
	Path string
}

// TryLock acquires the lock without blocking.
func TryLock(path string) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &FileLock{f: f, Path: path}, nil
}

// Lock acquires the lock, blocking until it is available.
func Lock(path string) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileLock{f: f, Path: path}, nil
}

func (l *FileLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	cerr := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return cerr
}

// HoldStateLock takes the state lock for the lifetime of a one-shot CLI
// command. WithStateLock then reuses it instead of deadlocking on a second
// flock of the same file.
func (s *Store) HoldStateLock() error {
	if s.held != nil {
		return nil
	}
	l, err := Lock(s.Path(FileStateLock))
	if err != nil {
		return err
	}
	s.held = l
	return nil
}

func (s *Store) ReleaseStateLock() {
	if s.held != nil {
		_ = s.held.Close()
		s.held = nil
	}
}

// WithStateLock serializes file transactions across processes.
func (s *Store) WithStateLock(fn func() error) error {
	if s.held != nil {
		return fn()
	}
	l, err := Lock(s.Path(FileStateLock))
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()
	return fn()
}
