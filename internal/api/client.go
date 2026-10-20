// Package api talks to the Stargate auth service: the RFC 8628 device flow,
// the direct challenge flow, session refresh and the session endpoints.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"src.solsynth.dev/sosys/termian/internal/deviceid"
)

// Defaults used when the caller leaves a field empty.
const (
	DefaultClientID = "solian"
	DefaultScope    = "*"
	// DefaultTimeout bounds a single HTTP round trip.
	DefaultTimeout = 15 * time.Second
	// gatewayPath is appended when the base URL carries no path.
	gatewayPath = "/stargate"
)

// Client is a Stargate API client.
type Client struct {
	// Base is the normalized API base, e.g. https://api.solian.app/stargate.
	Base *url.URL
	// ClientID is the OAuth client used by the device flow.
	ClientID string
	// Scope is the space-separated OAuth scope request.
	Scope string
	// Device describes this machine.
	Device deviceid.Identity
	// UA is the User-Agent header.
	UA string
	// HTTP performs requests.
	HTTP *http.Client
	// PollInterval overrides the server-supplied device poll interval; zero
	// means "use the server value". Tests set a small duration.
	PollInterval time.Duration

	// accessToken is the bearer token sent with authenticated requests; use
	// SetAccessToken to change it.
	accessToken string
	tokenMu     sync.RWMutex
}

// New builds a client for base.
func New(base, clientID, scope string, dev deviceid.Identity, version string) (*Client, error) {
	parsed, err := NormalizeBase(base)
	if err != nil {
		return nil, err
	}
	if clientID == "" {
		clientID = DefaultClientID
	}
	if scope == "" {
		scope = DefaultScope
	}
	if version == "" {
		version = "dev"
	}
	return &Client{
		Base:     parsed,
		ClientID: clientID,
		Scope:    scope,
		Device:   dev,
		UA:       fmt.Sprintf("Termian/%s (%s; %s)", version, runtime.GOOS, runtime.GOARCH),
		HTTP:     &http.Client{Timeout: DefaultTimeout},
	}, nil
}

// SetAccessToken publishes the bearer token used by subsequent requests.
func (c *Client) SetAccessToken(token string) {
	c.tokenMu.Lock()
	c.accessToken = token
	c.tokenMu.Unlock()
}

// AccessToken returns the bearer token currently in use.
func (c *Client) AccessToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.accessToken
}

// NormalizeBase validates the API base: an empty path gets "/stargate"
// appended, a trailing slash is trimmed.
func NormalizeBase(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("server address is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid server address %q: %w", raw, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid server scheme %q (want http or https)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("invalid server address %q: missing host", raw)
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = gatewayPath
	}
	parsed.Path = path
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

// endpoint resolves an API path against the base.
func (c *Client) endpoint(path string) string {
	base := *c.Base
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(path, "/")
	base.RawQuery = ""
	return base.String()
}

// doJSON performs a JSON request and decodes the response into out (if non-nil).
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	return c.do(ctx, method, path, "application/json", reader, out)
}

// doForm performs a form-urlencoded POST.
func (c *Client) doForm(ctx context.Context, path string, form url.Values, out any) error {
	return c.do(ctx, http.MethodPost, path, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), out)
}

// do executes the request and maps failures onto the package error types.
func (c *Client) do(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UA)
	if contentType != "" && body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if token := c.AccessToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := c.httpClient().Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return &TransportError{Err: err}
	}
	defer func() { _ = res.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return &TransportError{Err: err}
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return decodeError(res.StatusCode, payload)
	}
	if out == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return &MalformedResponseError{Path: path, Err: err}
	}
	return nil
}

// httpClient returns the configured HTTP client.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// deviceFields builds the shared device identity form fields.
func (c *Client) deviceFields() url.Values {
	form := url.Values{}
	if c.Device.ID != "" {
		form.Set("device_id", c.Device.ID)
	}
	if c.Device.Name != "" {
		form.Set("device_name", c.Device.Name)
	}
	if c.Device.Platform != 0 {
		form.Set("platform", fmt.Sprintf("%d", c.Device.Platform))
	}
	return form
}
