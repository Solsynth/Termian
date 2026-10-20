package api

import (
	"context"
	"errors"
	"net/http"
)

// Identity returns the authenticated identity (GET /auth/me).
func (c *Client) Identity(ctx context.Context) (Identity, error) {
	var id Identity
	if err := c.doJSON(ctx, http.MethodGet, "/auth/me", nil, &id); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// Account returns the authenticated account (GET /accounts/me).
func (c *Client) Account(ctx context.Context) (Account, error) {
	var acc Account
	if err := c.doJSON(ctx, http.MethodGet, "/accounts/me", nil, &acc); err != nil {
		return Account{}, err
	}
	return acc, nil
}

// RevokeSession revokes the current session (DELETE /sessions/current).
//
// A 401 counts as success: the session is already gone, which is the outcome
// logout wants.
func (c *Client) RevokeSession(ctx context.Context) error {
	err := c.doJSON(ctx, http.MethodDelete, "/sessions/current", nil, nil)
	if err == nil {
		return nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
		return nil
	}
	return err
}
