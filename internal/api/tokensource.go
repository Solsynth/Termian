package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"src.solsynth.dev/sosys/termian/internal/credentials"
)

// refreshSkew is how early an access token is renewed before it expires.
const refreshSkew = 60 * time.Second

// TokenSource owns the access token for one session and rotates the persisted
// refresh token when needed.
//
// Rotation is serialized: the server tolerates a duplicate refresh inside its
// grace window, not a stampede of them.
type TokenSource struct {
	mu sync.Mutex

	client *Client
	store  *credentials.Store
	creds  *credentials.Credentials

	access      string
	accessUntil time.Time
}

// NewTokenSource binds a client, its credential store and the stored record.
func NewTokenSource(c *Client, st *credentials.Store, creds *credentials.Credentials) *TokenSource {
	return &TokenSource{client: c, store: st, creds: creds}
}

// Credentials returns the current record (server, method, account snapshot).
func (t *TokenSource) Credentials() *credentials.Credentials {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.creds
}

// AccessExpiry reports when the current access token expires (zero if unknown).
func (t *TokenSource) AccessExpiry() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.accessUntil
}

// Access returns the access token currently held in memory.
func (t *TokenSource) Access() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.access
}

// Seed records an access token obtained by a fresh login.
func (t *TokenSource) Seed(creds *credentials.Credentials, access string, expiresIn int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.creds = creds
	t.access = access
	t.setExpiry(expiresIn)
	t.client.SetAccessToken(access)
}

// Token returns a usable access token, refreshing when fewer than refreshSkew
// remain.
func (t *TokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.creds == nil {
		return "", ErrSessionExpired
	}
	if t.access != "" && time.Until(t.accessUntil) > refreshSkew {
		return t.access, nil
	}
	if err := t.refreshLocked(ctx); err != nil {
		return "", err
	}
	return t.access, nil
}

// ForceRefresh rotates the refresh token unconditionally.
func (t *TokenSource) ForceRefresh(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.creds == nil {
		return ErrSessionExpired
	}
	return t.refreshLocked(ctx)
}

// refreshLocked performs one rotation. Callers must hold the mutex.
func (t *TokenSource) refreshLocked(ctx context.Context) error {
	creds := t.creds
	if creds == nil || creds.RefreshToken == "" {
		return ErrSessionExpired
	}

	var (
		access       string
		refreshToken string
		expiresIn    int64
	)
	refreshExpiresIn := int64(0)
	hasRefreshLife := false

	switch creds.Method {
	case credentials.MethodChallenge, credentials.MethodSSH:
		pair, err := t.client.RefreshChallenge(ctx, creds.RefreshToken)
		if err != nil {
			return t.handleRefreshError(err)
		}
		access, refreshToken, expiresIn = pair.Token, pair.RefreshToken, pair.ExpiresIn
		refreshExpiresIn, hasRefreshLife = pair.RefreshExpiresIn, true
	case credentials.MethodDevice, "":
		tok, err := t.client.RefreshOAuth(ctx, creds.RefreshToken)
		if err != nil {
			return t.handleRefreshError(err)
		}
		access, refreshToken, expiresIn = tok.AccessToken, tok.RefreshToken, tok.ExpiresIn
	default:
		return fmt.Errorf("unknown credential method %q", creds.Method)
	}

	now := time.Now()
	t.access = access
	t.setExpiry(expiresIn)
	t.client.SetAccessToken(access)

	rotated := *creds
	if refreshToken != "" {
		rotated.RefreshToken = refreshToken
	}
	if hasRefreshLife && refreshExpiresIn > 0 {
		rotated.RefreshExpiresAt = now.Add(time.Duration(refreshExpiresIn) * time.Second)
	}
	rotated.ObtainedAt = now

	saveErr := t.store.Save(&rotated)
	t.creds = &rotated
	if saveErr != nil {
		// The in-memory token keeps working for this process; surface the disk
		// problem so the user knows the rotation was not persisted.
		return fmt.Errorf("persist rotated refresh token: %w", saveErr)
	}
	return nil
}

// handleRefreshError converts a rejected rotation into ErrSessionExpired and
// drops the dead credentials.
func (t *TokenSource) handleRefreshError(err error) error {
	if !isExpiredSessionError(err) {
		return err
	}
	if t.creds != nil {
		_ = t.store.Delete(t.creds.Server)
	}
	t.creds = nil
	t.access = ""
	t.accessUntil = time.Time{}
	t.client.SetAccessToken("")
	return ErrSessionExpired
}

// setExpiry records the access token lifetime.
func (t *TokenSource) setExpiry(expiresIn int64) {
	if expiresIn <= 0 {
		t.accessUntil = time.Now().Add(5 * time.Minute)
		return
	}
	t.accessUntil = time.Now().Add(time.Duration(expiresIn) * time.Second)
}

// Logout revokes the server session (best effort) and deletes stored
// credentials.
func (t *TokenSource) Logout(ctx context.Context) error {
	t.mu.Lock()
	if t.creds == nil {
		t.mu.Unlock()
		return nil
	}
	server := t.creds.Server
	t.mu.Unlock()

	// Best effort: obtain an access token if needed, then revoke.
	var revokeErr error
	if _, err := t.Token(ctx); err == nil {
		t.mu.Lock()
		t.client.SetAccessToken(t.access)
		t.mu.Unlock()
		revokeErr = t.client.RevokeSession(ctx)
	} else if !errors.Is(err, ErrSessionExpired) {
		revokeErr = err
	}

	t.mu.Lock()
	t.access = ""
	t.accessUntil = time.Time{}
	t.creds = nil
	t.client.SetAccessToken("")
	t.mu.Unlock()

	if server == "" {
		return revokeErr
	}
	if deleteErr := t.store.Delete(server); deleteErr != nil {
		return errors.Join(revokeErr, deleteErr)
	}
	return revokeErr
}
