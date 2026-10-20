package api

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// deviceGrantType is the RFC 8628 grant type for device-code polling.
const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// RequestDeviceCode starts a device authorization (RFC 8628).
func (c *Client) RequestDeviceCode(ctx context.Context) (DeviceCode, error) {
	form := c.deviceFields()
	form.Set("client_id", c.ClientID)
	if c.Scope != "" {
		form.Set("scope", c.Scope)
	}

	var code DeviceCode
	if err := c.doForm(ctx, "/auth/open/device/code", form, &code); err != nil {
		return DeviceCode{}, err
	}
	if code.DeviceCode == "" || code.UserCode == "" {
		return DeviceCode{}, fmt.Errorf("device authorization response is missing device_code or user_code")
	}
	return code, nil
}

// PollDeviceToken asks the token endpoint whether the device code has been
// approved yet.
//
// The pending and slow_down responses are returned as *DeviceError so the caller
// can re-arm its poll timer; the caller adds five seconds for slow_down.
func (c *Client) PollDeviceToken(ctx context.Context, deviceCode string) (OAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", deviceGrantType)
	form.Set("device_code", deviceCode)
	form.Set("client_id", c.ClientID)

	var token OAuthToken
	err := c.doForm(ctx, "/auth/open/token", form, &token)
	if err != nil {
		return OAuthToken{}, err
	}
	if token.AccessToken == "" {
		return OAuthToken{}, &DeviceError{
			Code:        DeviceInvalidGrant,
			Description: "token response is missing access_token",
		}
	}
	return token, nil
}

// RefreshOAuth rotates a device-flow refresh token.
func (c *Client) RefreshOAuth(ctx context.Context, refreshToken string) (OAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", c.ClientID)

	var token OAuthToken
	if err := c.doForm(ctx, "/auth/open/token", form, &token); err != nil {
		return OAuthToken{}, err
	}
	if token.AccessToken == "" {
		return OAuthToken{}, &DeviceError{
			Code:        DeviceInvalidGrant,
			Description: "refresh response is missing access_token",
		}
	}
	return token, nil
}

// PollIntervalOrDefault returns the interval to wait between token polls.
func (c *Client) PollIntervalOrDefault(serverInterval int) time.Duration {
	if c.PollInterval > 0 {
		return c.PollInterval
	}
	if serverInterval <= 0 {
		return 5 * time.Second
	}
	return time.Duration(serverInterval) * time.Second
}
