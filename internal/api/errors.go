package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrSessionExpired reports that the stored refresh token is no longer
// accepted; the user has to sign in again.
var ErrSessionExpired = errors.New("session expired")

// ErrNoCredentials reports that a command needs a stored session but none
// exists.
var ErrNoCredentials = errors.New("not signed in")

// APIError is the Stargate error envelope: {code,message,status,detail,...}.
type APIError struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Status  int     `json:"status"`
	Detail  *string `json:"detail,omitempty"`
}

// Error renders the server message, preferring detail when present.
func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Code
	}
	if e.Detail != nil && *e.Detail != "" && *e.Detail != msg {
		msg += ": " + *e.Detail
	}
	if e.Status > 0 {
		return fmt.Sprintf("%s (HTTP %d)", msg, e.Status)
	}
	return msg
}

// DeviceError is the OAuth error shape used by the device flow endpoints.
type DeviceError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
	Status      int    `json:"-"`
}

// Error renders code and description.
func (e *DeviceError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	}
	return e.Code
}

// Device flow error codes that drive the poll loop.
const (
	DeviceAuthorizationPending = "authorization_pending"
	DeviceSlowDown             = "slow_down"
	DeviceExpiredToken         = "expired_token"
	DeviceAccessDenied         = "access_denied"
	DeviceInvalidGrant         = "invalid_grant"
	DeviceUnauthorizedClient   = "unauthorized_client"
	DeviceInvalidRequest       = "invalid_request"
)

// TransportError wraps dial/timeout/EOF failures so callers can distinguish
// "the network is down" from "the server said no".
type TransportError struct {
	Err error
}

// Error renders the underlying failure.
func (e *TransportError) Error() string { return "transport: " + e.Err.Error() }

// Unwrap exposes the wrapped error.
func (e *TransportError) Unwrap() error { return e.Err }

// MalformedResponseError reports a 2xx body that does not decode as the
// expected JSON; it means the endpoint is wrong or a proxy intervened.
type MalformedResponseError struct {
	Path string
	Err  error
}

// Error renders the failing path and cause.
func (e *MalformedResponseError) Error() string {
	return fmt.Sprintf("malformed response from %s: %v", e.Path, e.Err)
}

// Unwrap exposes the decoding error.
func (e *MalformedResponseError) Unwrap() error { return e.Err }

// IsCode reports whether err is an APIError with the given code.
func IsCode(err error, code string) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code == code
	}
	return false
}

// IsDeviceCode reports whether err is a DeviceError with the given code.
func IsDeviceCode(err error, code string) bool {
	var devErr *DeviceError
	if errors.As(err, &devErr) {
		return devErr.Code == code
	}
	return false
}

// isExpiredSessionError classifies refresh failures that mean "sign in again".
func isExpiredSessionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSessionExpired) {
		return true
	}
	if IsDeviceCode(err, DeviceInvalidGrant) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "AUTH_REFRESH_FAILED", "AUTH_REFRESH_TOKEN_REQUIRED", "TOKEN_EXPIRED", "AUTH_UNAUTHORIZED":
			return true
		}
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "refresh token") ||
		strings.Contains(msg, "session was not found") ||
		strings.Contains(msg, "session has been expired") ||
		strings.Contains(msg, "has been revoked")
}

// decodeError turns a non-2xx response body into the most precise error type.
func decodeError(status int, body []byte) error {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return &APIError{
			Code:   "HTTP_" + strconv.Itoa(status),
			Status: status,
		}
	}

	var envelope APIError
	if err := json.Unmarshal(body, &envelope); err == nil && (envelope.Code != "" || envelope.Message != "") {
		envelope.Status = status
		if envelope.Code == "" {
			envelope.Code = "HTTP_" + strconv.Itoa(status)
		}
		return &envelope
	}

	var oauth DeviceError
	if err := json.Unmarshal(body, &oauth); err == nil && oauth.Code != "" {
		oauth.Status = status
		return &oauth
	}

	return &APIError{
		Code:    "HTTP_" + strconv.Itoa(status),
		Message: trimmed,
		Status:  status,
	}
}
