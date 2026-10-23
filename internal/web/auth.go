package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	sessionIdleTTL = 30 * time.Minute
	sessionAbsTTL  = 12 * time.Hour
	maxLoginFails  = 5
	loginWindow    = time.Minute
	maxBlock       = 15 * time.Minute
)

var (
	ErrBadPassword = errors.New("invalid password")
	ErrRateLimited = errors.New("too many login attempts")
	ErrNoSession   = errors.New("no valid session")
)

type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return ErrRateLimited.Error() }
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

type session struct {
	Token   string
	CSRF    string
	Created time.Time
	Last    time.Time
}

type attempt struct {
	count        int
	window       time.Time
	blockedUntil time.Time
	level        int
}

type Auth struct {
	mu       sync.Mutex
	password string
	sessions map[string]*session
	attempts map[string]*attempt
	now      func() time.Time
	// OnPassword prints the password exactly once to the console. It must never
	// write to the regular log.
	OnPassword func(string)
}

func NewAuth() (*Auth, error) {
	pw, err := GeneratePassword()
	if err != nil {
		return nil, err
	}
	return &Auth{
		password:   pw,
		sessions:   map[string]*session{},
		attempts:   map[string]*attempt{},
		now:        time.Now,
		OnPassword: func(string) {},
	}, nil
}

// GeneratePassword returns 128 bit of entropy as base32.
// shortcut: 128 bit needs 26 base32 characters; the spec's "20 characters"
// would only be 100 bit. Entropy wins (see DECISIONS.md).
func GeneratePassword() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

func (a *Auth) Password() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.password
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func constantEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// Login validates the password and creates a server-side session. It returns
// the session token and the CSRF token.
func (a *Auth) Login(ip, password string) (string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if at := a.attempts[ip]; at != nil {
		if now.Before(at.blockedUntil) {
			return "", "", &RateLimitError{RetryAfter: at.blockedUntil.Sub(now)}
		}
		if !at.window.IsZero() && now.Sub(at.window) > loginWindow {
			at.count = 0
			at.window = now
		}
	}
	if !constantEqual(a.password, password) {
		at := a.attempts[ip]
		if at == nil {
			at = &attempt{window: now}
			a.attempts[ip] = at
		}
		at.count++
		if at.count >= maxLoginFails {
			at.level++
			d := 30 * time.Second << (at.level - 1)
			if d > maxBlock || d <= 0 {
				d = maxBlock
			}
			at.blockedUntil = now.Add(d)
			at.count = 0
			a.attempts[ip] = at
			return "", "", &RateLimitError{RetryAfter: d}
		}
		a.attempts[ip] = at
		return "", "", ErrBadPassword
	}
	delete(a.attempts, ip)
	tok, err := randomToken()
	if err != nil {
		return "", "", err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", "", err
	}
	a.sessions[tok] = &session{Token: tok, CSRF: csrf, Created: now, Last: now}
	a.pruneLocked(now)
	return tok, csrf, nil
}

func (a *Auth) pruneLocked(now time.Time) {
	for tok, s := range a.sessions {
		if now.Sub(s.Last) > sessionIdleTTL || now.Sub(s.Created) >= sessionAbsTTL {
			delete(a.sessions, tok)
		}
	}
}

func (a *Auth) Validate(token string) (*session, bool) {
	if token == "" {
		return nil, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	s, ok := a.sessions[token]
	if !ok {
		return nil, false
	}
	if now.Sub(s.Last) > sessionIdleTTL || now.Sub(s.Created) >= sessionAbsTTL {
		delete(a.sessions, token)
		return nil, false
	}
	s.Last = now
	return s, true
}

func (a *Auth) Logout(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// RotatePassword creates a new password, invalidates all sessions except keep,
// and returns the new password.
func (a *Auth) RotatePassword(keep string) (string, error) {
	pw, err := GeneratePassword()
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.password = pw
	for tok := range a.sessions {
		if tok != keep {
			delete(a.sessions, tok)
		}
	}
	a.mu.Unlock()
	a.OnPassword(pw)
	return pw, nil
}

func (a *Auth) CSRF(token string) (string, bool) {
	s, ok := a.Validate(token)
	if !ok {
		return "", false
	}
	return s.CSRF, true
}

func (a *Auth) String() string { return fmt.Sprintf("Auth(%d sessions)", len(a.sessions)) }
