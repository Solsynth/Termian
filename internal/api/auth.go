package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// challengeRequest is the POST /auth/challenge body.
type challengeRequest struct {
	Account    string   `json:"account"`
	DeviceID   string   `json:"device_id"`
	DeviceName string   `json:"device_name,omitempty"`
	Platform   int      `json:"platform"`
	Audiences  []string `json:"audiences"`
	Scopes     []string `json:"scopes"`
}

// factorSubmitRequest is the PATCH /auth/challenge/{id} body.
type factorSubmitRequest struct {
	FactorID string `json:"factor_id"`
	Password string `json:"password"`
}

// tokenRequest is the POST /auth/token body.
type tokenRequest struct {
	GrantType    string `json:"grant_type"`
	Code         string `json:"code,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// CreateChallenge opens (or reuses) a login challenge for account.
func (c *Client) CreateChallenge(ctx context.Context, account string) (Challenge, error) {
	req := challengeRequest{
		Account:    account,
		DeviceID:   c.Device.ID,
		DeviceName: c.Device.Name,
		Platform:   c.Device.Platform,
		Audiences:  []string{},
		Scopes:     []string{},
	}
	var ch Challenge
	if err := c.doJSON(ctx, http.MethodPost, "/auth/challenge", req, &ch); err != nil {
		return Challenge{}, err
	}
	if ch.ID == "" {
		return Challenge{}, fmt.Errorf("challenge response is missing id")
	}
	return ch, nil
}

// Challenge re-reads a challenge, used to poll for cross-device approval.
func (c *Client) Challenge(ctx context.Context, id string) (Challenge, error) {
	var ch Challenge
	if err := c.doJSON(ctx, http.MethodGet, "/auth/challenge/"+url.PathEscape(id), nil, &ch); err != nil {
		return Challenge{}, err
	}
	return ch, nil
}

// Factors lists the login factors the server will accept for a challenge.
func (c *Client) Factors(ctx context.Context, challengeID string) ([]Factor, error) {
	var factors []Factor
	path := "/auth/challenge/" + url.PathEscape(challengeID) + "/factors"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &factors); err != nil {
		return nil, err
	}
	return factors, nil
}

// RequestFactorCode triggers an out-of-band code (email) for a factor.
func (c *Client) RequestFactorCode(ctx context.Context, challengeID, factorID string) error {
	path := "/auth/challenge/" + url.PathEscape(challengeID) + "/factors/" + url.PathEscape(factorID)
	return c.doJSON(ctx, http.MethodPost, path, struct{}{}, nil)
}

// SubmitFactor verifies one factor and returns the updated challenge.
func (c *Client) SubmitFactor(ctx context.Context, challengeID, factorID, secret string) (Challenge, error) {
	path := "/auth/challenge/" + url.PathEscape(challengeID)
	req := factorSubmitRequest{FactorID: factorID, Password: secret}
	var ch Challenge
	if err := c.doJSON(ctx, http.MethodPatch, path, req, &ch); err != nil {
		return Challenge{}, err
	}
	return ch, nil
}

// ExchangeChallenge trades a satisfied challenge for a token pair.
func (c *Client) ExchangeChallenge(ctx context.Context, challengeID string) (TokenPair, error) {
	req := tokenRequest{GrantType: "authorization_code", Code: challengeID}
	var pair TokenPair
	if err := c.doJSON(ctx, http.MethodPost, "/auth/token", req, &pair); err != nil {
		return TokenPair{}, err
	}
	if pair.Token == "" {
		return TokenPair{}, fmt.Errorf("token exchange response is missing token")
	}
	return pair, nil
}

// RefreshChallenge rotates a challenge-flow refresh token.
func (c *Client) RefreshChallenge(ctx context.Context, refreshToken string) (TokenPair, error) {
	req := tokenRequest{GrantType: "refresh_token", RefreshToken: refreshToken}
	var pair TokenPair
	if err := c.doJSON(ctx, http.MethodPost, "/auth/token", req, &pair); err != nil {
		return TokenPair{}, err
	}
	if pair.Token == "" {
		return TokenPair{}, fmt.Errorf("refresh response is missing token")
	}
	return pair, nil
}
