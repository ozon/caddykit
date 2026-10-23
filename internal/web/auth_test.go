package web

import (
	"errors"
	"testing"
	"time"
)

func newTestAuth(t *testing.T) (*Auth, *time.Time) {
	t.Helper()
	a, err := NewAuth()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	return a, &now
}

func TestWrongPasswordAndRateLimit(t *testing.T) {
	a, now := newTestAuth(t)
	for i := 0; i < maxLoginFails-1; i++ {
		if _, _, err := a.Login("1.2.3.4", "wrong"); !errors.Is(err, ErrBadPassword) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, _, err := a.Login("1.2.3.4", "wrong"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit, got %v", err)
	}
	// Still blocked before the retry delay.
	if _, _, err := a.Login("1.2.3.4", a.Password()); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected block even for the correct password, got %v", err)
	}
	*now = now.Add(31 * time.Second)
	if _, _, err := a.Login("1.2.3.4", a.Password()); err != nil {
		t.Fatalf("block should expire: %v", err)
	}
	// A different IP is unaffected.
	if _, _, err := a.Login("5.6.7.8", a.Password()); err != nil {
		t.Fatalf("other IP blocked: %v", err)
	}
}

func TestSessionIdleAndAbsoluteTTL(t *testing.T) {
	a, now := newTestAuth(t)
	tok, csrf, err := a.Login("ip", a.Password())
	if err != nil {
		t.Fatal(err)
	}
	if csrf == "" {
		t.Fatal("empty CSRF")
	}
	if _, ok := a.Validate(tok); !ok {
		t.Fatal("session should be valid")
	}
	*now = now.Add(20 * time.Minute)
	if _, ok := a.Validate(tok); !ok {
		t.Fatal("session should survive within idle TTL")
	}
	*now = now.Add(31 * time.Minute)
	if _, ok := a.Validate(tok); ok {
		t.Fatal("idle session should expire")
	}

	tok2, _, _ := a.Login("ip", a.Password())
	for i := 0; i < 24; i++ {
		*now = now.Add(30 * time.Minute)
		a.Validate(tok2)
	}
	if _, ok := a.Validate(tok2); ok {
		t.Fatal("absolute TTL must end the session")
	}
}

func TestRotateInvalidatesOtherSessions(t *testing.T) {
	a, _ := newTestAuth(t)
	tok1, _, _ := a.Login("ip1", a.Password())
	tok2, _, _ := a.Login("ip2", a.Password())
	old := a.Password()
	pw, err := a.RotatePassword(tok1)
	if err != nil {
		t.Fatal(err)
	}
	if pw == old {
		t.Fatal("password did not change")
	}
	if _, ok := a.Validate(tok2); ok {
		t.Fatal("other session must be invalidated")
	}
	if _, ok := a.Validate(tok1); !ok {
		t.Fatal("current session must survive rotation")
	}
	if _, _, err := a.Login("ip3", old); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("old password still valid: %v", err)
	}
	if _, _, err := a.Login("ip3", pw); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
}

func TestPasswordEntropyAndAlphabet(t *testing.T) {
	pw, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(pw) != 26 {
		t.Fatalf("expected 26 base32 chars for 128 bit, got %d (%q)", len(pw), pw)
	}
	for _, r := range pw {
		valid := (r >= 'A' && r <= 'Z') || (r >= '2' && r <= '7')
		if !valid {
			t.Fatalf("ambiguous character %q", r)
		}
	}
}

func TestConstantEqual(t *testing.T) {
	if !constantEqual("abc", "abc") || constantEqual("abc", "abd") {
		t.Fatal("constantEqual misbehaves")
	}
}
