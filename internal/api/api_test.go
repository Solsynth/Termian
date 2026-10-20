package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"src.solsynth.dev/sosys/termian/internal/credentials"
	"src.solsynth.dev/sosys/termian/internal/deviceid"
)

// testDevice is the device identity used by the tests.
var testDevice = deviceid.Identity{ID: "device-abc", Name: "test-host", Platform: deviceid.PlatformLinux}

// newTestClient builds a client pointed at a test server.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := New(server.URL, "solian", "*", testDevice, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.HTTP = server.Client()
	return client, server
}

// TestRequestDeviceCodeSendsFormFields pins the wire contract: the endpoint only
// reads form values, and the device identity travels with the request.
func TestRequestDeviceCodeSendsFormFields(t *testing.T) {
	var form url.Values
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/auth/open/device/code") {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"device_code":"dc-1","user_code":"LHJC-ZBPP",
			"verification_uri":"https://id.solian.app/auth/device",
			"verification_uri_complete":"https://id.solian.app/auth/device?code=LHJC-ZBPP",
			"expires_in":600,"interval":5}`)
	})

	code, err := client.RequestDeviceCode(context.Background())
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if code.UserCode != "LHJC-ZBPP" || code.DeviceCode != "dc-1" || code.ExpiresIn != 600 || code.Interval != 5 {
		t.Fatalf("DeviceCode = %+v", code)
	}
	for field, want := range map[string]string{
		"client_id":   "solian",
		"scope":       "*",
		"device_id":   "device-abc",
		"device_name": "test-host",
		"platform":    "6",
	} {
		if got := form.Get(field); got != want {
			t.Errorf("form %s = %q, want %q", field, got, want)
		}
	}
}

// TestPollDeviceTokenClassifiesErrors covers every branch of the poll loop's
// classification: the two retryable codes surface as *DeviceError, the terminal
// ones stay distinguishable.
func TestPollDeviceTokenClassifiesErrors(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantCode string
		retry    bool
	}{
		{"pending", 400, `{"error":"authorization_pending","error_description":"Authorization pending."}`, DeviceAuthorizationPending, true},
		{"slow down", 400, `{"error":"slow_down","error_description":"Slow down."}`, DeviceSlowDown, true},
		{"expired", 400, `{"error":"expired_token","error_description":"Expired."}`, DeviceExpiredToken, false},
		{"denied", 400, `{"error":"access_denied","error_description":"Denied."}`, DeviceAccessDenied, false},
		{"invalid grant", 400, `{"error":"invalid_grant","error_description":"Invalid."}`, DeviceInvalidGrant, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})

			_, err := client.PollDeviceToken(context.Background(), "dc-1")
			if !IsDeviceCode(err, tc.wantCode) {
				t.Fatalf("err = %v, want DeviceError %q", err, tc.wantCode)
			}
			if retryable := IsDeviceCode(err, DeviceAuthorizationPending) || IsDeviceCode(err, DeviceSlowDown); retryable != tc.retry {
				t.Fatalf("retryable = %v, want %v", retryable, tc.retry)
			}
		})
	}
}

// TestPollDeviceTokenSuccessAndRefreshForm covers the happy path and the refresh
// grant, which must repeat client_id for public clients.
func TestPollDeviceTokenSuccessAndRefreshForm(t *testing.T) {
	var grants []url.Values
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		grants = append(grants, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"at-1","refresh_token":"rt-1","token_type":"Bearer","expires_in":300}`)
	})

	tok, err := client.PollDeviceToken(context.Background(), "dc-1")
	if err != nil {
		t.Fatalf("PollDeviceToken: %v", err)
	}
	if tok.AccessToken != "at-1" || tok.RefreshToken != "rt-1" || tok.ExpiresIn != 300 {
		t.Fatalf("token = %+v", tok)
	}

	rotated, err := client.RefreshOAuth(context.Background(), "rt-1")
	if err != nil {
		t.Fatalf("RefreshOAuth: %v", err)
	}
	if rotated.AccessToken != "at-1" {
		t.Fatalf("refresh token response = %+v", rotated)
	}

	if len(grants) != 2 {
		t.Fatalf("requests = %d, want 2", len(grants))
	}
	if got := grants[0].Get("grant_type"); got != deviceGrantType {
		t.Errorf("poll grant_type = %q, want %q", got, deviceGrantType)
	}
	if got := grants[0].Get("device_code"); got != "dc-1" {
		t.Errorf("poll device_code = %q", got)
	}
	if got := grants[1].Get("grant_type"); got != "refresh_token" {
		t.Errorf("refresh grant_type = %q", got)
	}
	if got := grants[1].Get("client_id"); got != "solian" {
		t.Errorf("refresh client_id = %q", got)
	}
}

// TestChallengeFlowWireContract pins the challenge calls: JSON bodies, the PATCH
// factor submission and the error envelope decoding.
func TestChallengeFlowWireContract(t *testing.T) {
	var created map[string]any
	var submitted map[string]string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/stargate/auth/challenge" && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &created)
			io.WriteString(w, `{"id":"ch-1","step_total":2,"step_remain":2,"blacklist_factors":[],"account_id":"acc-1"}`)
		case strings.HasSuffix(r.URL.Path, "/factors") && r.Method == http.MethodGet:
			io.WriteString(w, `[{"id":"f-pw","type":0,"trustworthy":2},{"id":"f-pk","type":7,"trustworthy":2}]`)
		case r.Method == http.MethodPatch:
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &submitted)
			if submitted["password"] == "wrong" {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"code":"AUTH_INVALID_PASSWORD","message":"Invalid password.","status":400}`)
				return
			}
			io.WriteString(w, `{"id":"ch-1","step_total":2,"step_remain":1,"blacklist_factors":["f-pw"]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	ch, err := client.CreateChallenge(context.Background(), "alice")
	if err != nil {
		t.Fatalf("CreateChallenge: %v", err)
	}
	if ch.ID != "ch-1" || ch.StepRemain != 2 {
		t.Fatalf("challenge = %+v", ch)
	}
	if created["account"] != "alice" || created["device_id"] != "device-abc" {
		t.Fatalf("challenge request body = %+v", created)
	}
	if platform, ok := created["platform"].(float64); !ok || int(platform) != 6 {
		t.Fatalf("challenge platform = %v", created["platform"])
	}

	factors, err := client.Factors(context.Background(), "ch-1")
	if err != nil {
		t.Fatalf("Factors: %v", err)
	}
	if len(factors) != 2 || factors[1].Type != FactorPasskey || factors[0].Usable() != true || factors[1].Usable() {
		t.Fatalf("factors = %+v", factors)
	}

	_, err = client.SubmitFactor(context.Background(), "ch-1", "f-pw", "wrong")
	if !IsCode(err, "AUTH_INVALID_PASSWORD") {
		t.Fatalf("SubmitFactor error = %v, want AUTH_INVALID_PASSWORD", err)
	}
	if submitted["factor_id"] != "f-pw" || submitted["password"] != "wrong" {
		t.Fatalf("submit body = %+v", submitted)
	}

	next, err := client.SubmitFactor(context.Background(), "ch-1", "f-pw", "correct")
	if err != nil {
		t.Fatalf("SubmitFactor: %v", err)
	}
	if next.StepRemain != 1 || len(next.BlacklistFactors) != 1 {
		t.Fatalf("updated challenge = %+v", next)
	}
}

// TestRevokeSessionTreatsUnauthorizedAsSuccess keeps logout idempotent.
func TestRevokeSessionTreatsUnauthorizedAsSuccess(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusUnauthorized} {
		client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete || r.URL.Path != "/stargate/sessions/current" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer at-1" {
				t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(status)
		})
		client.SetAccessToken("at-1")
		if err := client.RevokeSession(context.Background()); err != nil {
			t.Fatalf("status %d: RevokeSession = %v", status, err)
		}
	}
}

// TestNormalizeBase pins the gateway path handling.
func TestNormalizeBase(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://api.solian.app", "https://api.solian.app/stargate"},
		{"https://api.solian.app/", "https://api.solian.app/stargate"},
		{"https://api.solian.app/stargate/", "https://api.solian.app/stargate"},
		{"api.solian.app", "https://api.solian.app/stargate"},
		{"http://127.0.0.1:8080/api", "http://127.0.0.1:8080/api"},
	}
	for _, tc := range cases {
		got, err := NormalizeBase(tc.in)
		if err != nil {
			t.Fatalf("NormalizeBase(%q): %v", tc.in, err)
		}
		if got.String() != tc.want {
			t.Errorf("NormalizeBase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := NormalizeBase("ftp://example.com"); err == nil {
		t.Error("NormalizeBase accepted a non-HTTP scheme")
	}
}

// TestDevicePollLoopRetriesThenSucceeds drives the poll loop the CLI runs:
// two authorization_pending answers, one slow_down, then a token.
func TestDevicePollLoopRetriesThenSucceeds(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1, 2:
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"authorization_pending"}`)
		case 3:
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"slow_down"}`)
		default:
			io.WriteString(w, `{"access_token":"at-1","refresh_token":"rt-1","expires_in":300}`)
		}
	})
	client.PollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	interval := client.PollIntervalOrDefault(5)
	pending := 0
	var tok OAuthToken
	for {
		var err error
		tok, err = client.PollDeviceToken(ctx, "dc-1")
		if err == nil {
			break
		}
		switch {
		case IsDeviceCode(err, DeviceAuthorizationPending):
			pending++
		case IsDeviceCode(err, DeviceSlowDown):
			interval += 5 * time.Second
			interval = client.PollInterval // tests do not wait the real interval
		default:
			t.Fatalf("unexpected poll error: %v", err)
		}
		if pending > 5 {
			t.Fatal("poll loop did not terminate")
		}
	}

	if pending != 2 {
		t.Fatalf("pending answers = %d, want 2", pending)
	}
	if tok.AccessToken != "at-1" {
		t.Fatalf("token = %+v", tok)
	}
}

// TestTransportErrorIsTyped keeps dial failures distinguishable from API errors.
func TestTransportErrorIsTyped(t *testing.T) {
	client, err := New("http://127.0.0.1:1", "solian", "*", testDevice, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.HTTP = &http.Client{Timeout: 200 * time.Millisecond}

	_, err = client.RequestDeviceCode(context.Background())
	var transport *TransportError
	if !errors.As(err, &transport) {
		t.Fatalf("err = %v, want *TransportError", err)
	}
}

// TestTokenSourceSingleFlightAndRotation covers the two invariants of rotation:
// one refresh for concurrent callers, and the rotated token persisted.
func TestTokenSourceSingleFlightAndRotation(t *testing.T) {
	store, err := credentials.Open(credentials.ModeFile, t.TempDir())
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	server := "https://api.solian.app/stargate"
	stored := &credentials.Credentials{
		Server:       server,
		Method:       credentials.MethodDevice,
		ClientID:     "solian",
		DeviceID:     testDevice.ID,
		RefreshToken: "rt-0",
	}
	if err := store.Save(stored); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var mu sync.Mutex
	refreshCalls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		refreshCalls++
		n := refreshCalls
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"at-`+itoa(n)+`","refresh_token":"rt-`+itoa(n)+`","expires_in":300}`)
	})

	// The stored record keys on the live test server, not the real API.
	stored.Server = client.Base.String()
	if err := store.Save(stored); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tokens := NewTokenSource(client, store, stored)

	const callers = 8
	var wg sync.WaitGroup
	results := make([]string, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = tokens.Token(context.Background())
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	mu.Lock()
	calls := refreshCalls
	mu.Unlock()
	if calls != 1 {
		t.Fatalf("refresh requests = %d, want exactly 1", calls)
	}
	for i, got := range results {
		if got != "at-1" {
			t.Fatalf("caller %d token = %q, want at-1", i, got)
		}
	}

	persisted, err := store.Load(client.Base.String())
	if err != nil || persisted == nil {
		t.Fatalf("Load rotated credentials: %+v, %v", persisted, err)
	}
	if persisted.RefreshToken != "rt-1" {
		t.Fatalf("persisted refresh token = %q, want rt-1", persisted.RefreshToken)
	}

	// A second rotation must be picked up on request.
	if err := tokens.ForceRefresh(context.Background()); err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if got := tokens.Access(); got != "at-2" {
		t.Fatalf("access after ForceRefresh = %q, want at-2", got)
	}
}

// TestTokenSourceRejectedRefreshClearsCredentials covers the dead-session path.
func TestTokenSourceRejectedRefreshClearsCredentials(t *testing.T) {
	store, err := credentials.Open(credentials.ModeFile, t.TempDir())
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"invalid_grant","error_description":"Invalid refresh token."}`)
	})

	stored := &credentials.Credentials{
		Server:       client.Base.String(),
		Method:       credentials.MethodDevice,
		ClientID:     "solian",
		RefreshToken: "rt-dead",
	}
	if err := store.Save(stored); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tokens := NewTokenSource(client, store, stored)
	if _, err := tokens.Token(context.Background()); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("Token error = %v, want ErrSessionExpired", err)
	}
	if tokens.Credentials() != nil {
		t.Fatal("credentials survived a rejected refresh")
	}
	remaining, err := store.Load(stored.Server)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if remaining != nil {
		t.Fatalf("stored credentials = %+v, want them deleted", remaining)
	}
}

// TestTokenSourceChallengeMethodUsesChallengeEndpoint pins endpoint selection by
// credential method.
func TestTokenSourceChallengeMethodUsesChallengeEndpoint(t *testing.T) {
	store, err := credentials.Open(credentials.ModeFile, t.TempDir())
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stargate/auth/token" {
			t.Errorf("path = %q, want the challenge token endpoint", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"token":"at-1","refresh_token":"rt-1","expires_in":300,"refresh_expires_in":2592000}`)
	})

	stored := &credentials.Credentials{
		Server:       client.Base.String(),
		Method:       credentials.MethodChallenge,
		RefreshToken: "rt-0",
	}
	tokens := NewTokenSource(client, store, stored)
	if err := tokens.ForceRefresh(context.Background()); err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}

	rotated := tokens.Credentials()
	if rotated == nil || rotated.RefreshExpiresAt.IsZero() {
		t.Fatalf("challenge rotation must record the refresh lifetime: %+v", rotated)
	}
	if rotated.RefreshExpiresAt.Before(time.Now().Add(24 * time.Hour)) {
		t.Fatalf("RefreshExpiresAt = %v, want ~30 days out", rotated.RefreshExpiresAt)
	}
}

// itoa avoids importing strconv for one call site in the fixtures.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
