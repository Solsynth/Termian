// Package ui renders command output and maps errors onto exit codes.
package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// Mode selects the output format.
type Mode string

// Output modes.
const (
	ModeText Mode = "text"
	ModeJSON Mode = "json"
)

// Record types emitted as the "type" field of every JSON record.
const (
	RecordAuthorization = "authorization"
	RecordAuthenticated = "authenticated"
	RecordAccount       = "account"
	RecordSession       = "session"
	RecordRefreshed     = "refreshed"
	RecordLoggedOut     = "logged_out"
	RecordError         = "error"

	// Content records: the timeline, posts and chats.
	RecordTimeline    = "timeline"
	RecordPost        = "post"
	RecordReply       = "reply"
	RecordCreated     = "created"
	RecordBookmark    = "bookmark"
	RecordReaction    = "reaction"
	RecordPublisher   = "publisher"
	RecordChatRoom    = "chat_room"
	RecordChatMessage = "chat_message"
	RecordChatSent    = "chat_sent"
)

// UsageError reports an invalid invocation, so callers can map it to exit 2
// without importing CLI types.
type UsageError struct {
	Message string
}

// Error implements error.
func (e *UsageError) Error() string { return e.Message }

// Exit codes.
const (
	ExitOK          = 0
	ExitUsage       = 2
	ExitAuthMissing = 3
	ExitTransport   = 4
	ExitServer      = 5
	ExitRateLimited = 6
	ExitDenied      = 7
	ExitFailure     = 1
)

// Renderer writes human or NDJSON output.
type Renderer struct {
	// Out receives payloads.
	Out io.Writer
	// Err receives progress lines and error records.
	Err io.Writer
	// Mode selects text or JSON.
	Mode Mode
}

// New builds a renderer.
func New(out, err io.Writer, mode string) *Renderer {
	switch Mode(mode) {
	case ModeJSON:
		return &Renderer{Out: out, Err: err, Mode: ModeJSON}
	default:
		return &Renderer{Out: out, Err: err, Mode: ModeText}
	}
}

// JSON reports whether the renderer emits NDJSON.
func (r *Renderer) JSON() bool { return r.Mode == ModeJSON }

// Emit writes v as one compact NDJSON line in JSON mode; text mode is a no-op.
func (r *Renderer) Emit(v any) error {
	if !r.JSON() {
		return nil
	}
	enc := json.NewEncoder(r.Out)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// Textf writes a progress line to stderr in text mode; JSON mode is a no-op so
// stdout stays machine readable.
func (r *Renderer) Textf(format string, a ...any) {
	if r.JSON() {
		return
	}
	fmt.Fprintf(r.Err, format+"\n", a...)
}

// Progressf writes a progress line to stderr in every mode. Callers must never
// include secrets.
func (r *Renderer) Progressf(format string, a ...any) {
	fmt.Fprintf(r.Err, format+"\n", a...)
}

// Table prints an aligned table in text mode. In JSON mode the rows are emitted
// as `{"type": record, "fields": {...}}` records instead.
func (r *Renderer) Table(record string, headers []string, rows [][]string) error {
	if r.JSON() {
		for _, row := range rows {
			fields := make(map[string]string, len(headers))
			for i, h := range headers {
				if i < len(row) {
					fields[strings.ToLower(h)] = row[i]
				}
			}
			if err := r.Emit(map[string]any{"type": record, "fields": fields}); err != nil {
				return err
			}
		}
		return nil
	}

	tw := tabwriter.NewWriter(r.Out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(headers, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// ErrorRecord is the JSON shape written for failures.
type ErrorRecord struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// Fail reports err on stderr and returns the process exit code.
func Fail(r *Renderer, err error) int {
	if err == nil {
		return ExitOK
	}

	code, status, message := classify(err)
	rec := ErrorRecord{
		Type:    RecordError,
		Code:    code,
		Message: message,
		Status:  status,
		Hint:    hint(code, err),
	}

	if r == nil {
		// No renderer configured: still map the exit code.
		return exitFor(code, status, err)
	}

	if r.JSON() {
		enc := json.NewEncoder(r.Err)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(rec)
	} else {
		line := "error: " + message
		if code != "" && code != "USAGE" {
			line = "error: " + code + ": " + message
		}
		if rec.Hint != "" {
			line += "\n  hint: " + rec.Hint
		}
		fmt.Fprintln(r.Err, line)
	}
	return exitFor(code, status, err)
}

// classify extracts the code, HTTP status and message from err.
func classify(err error) (string, int, string) {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		msg := apiErr.Message
		if msg == "" {
			msg = apiErr.Code
		}
		return apiErr.Code, apiErr.Status, msg
	}

	var devErr *api.DeviceError
	if errors.As(err, &devErr) {
		msg := devErr.Description
		if msg == "" {
			msg = devErr.Code
		}
		return devErr.Code, devErr.Status, msg
	}

	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		return "USAGE", 0, usageErr.Message
	}

	var trErr *api.TransportError
	if errors.As(err, &trErr) {
		return "TRANSPORT_ERROR", 0, trErr.Error()
	}

	var malErr *api.MalformedResponseError
	if errors.As(err, &malErr) {
		return "MALFORMED_RESPONSE", http.StatusBadGateway, malErr.Error()
	}

	if errors.Is(err, api.ErrSessionExpired) {
		return "SESSION_EXPIRED", 0, "session expired"
	}

	if errors.Is(err, api.ErrNoCredentials) {
		return "NO_CREDENTIALS", 0, err.Error()
	}

	return "ERROR", 0, err.Error()
}

// exitFor maps a classified error onto its exit code.
func exitFor(code string, status int, err error) int {
	switch {
	case code == "USAGE":
		return ExitUsage
	case code == "ERROR":
		return ExitFailure
	case errors.Is(err, api.ErrSessionExpired), code == "SESSION_EXPIRED", code == "NO_CREDENTIALS":
		return ExitAuthMissing
	case code == "TRANSPORT_ERROR":
		return ExitTransport
	case code == "RATE_LIMITED":
		return ExitRateLimited
	case code == "expired_token", code == "access_denied", code == "invalid_grant", code == "unauthorized_client":
		return ExitDenied
	}
	if status >= http.StatusInternalServerError {
		return ExitServer
	}
	return ExitFailure
}

// hint returns an actionable next step for known failures.
func hint(code string, err error) string {
	var apiErr *api.APIError
	detail := ""
	if errors.As(err, &apiErr) && apiErr.Detail != nil {
		detail = *apiErr.Detail
	}

	switch code {
	case "AUTH_ACCOUNT_NOT_FOUND":
		return "check the username or email"
	case "AUTH_INVALID_PASSWORD":
		return "wrong password/code; attempts are limited"
	case "ACCOUNT_LOCKED":
		if detail != "" {
			return detail
		}
		return "this account is locked"
	case "NO_AUTH_FACTORS":
		return "this account has no usable login factor — use device login"
	case "RATE_LIMITED":
		return "wait and retry"
	case "invalid_grant":
		return "device code expired — run the login again"
	case "expired_token":
		return "device code expired — run the login again"
	case "access_denied":
		return "the sign-in request was denied"
	case "SESSION_EXPIRED", "NO_CREDENTIALS":
		return "session expired — run `termian login`"
	case "TRANSPORT_ERROR":
		return "check the network connection and the server address"
	}
	return ""
}
