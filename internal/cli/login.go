package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/credentials"
	"src.solsynth.dev/sosys/termian/internal/ui"
)

// authzRecord is the "authorization" NDJSON record of a device login.
type authzRecord struct {
	Type                    string    `json:"type"`
	UserCode                string    `json:"user_code"`
	VerificationURI         string    `json:"verification_uri"`
	VerificationURIComplete string    `json:"verification_uri_complete,omitempty"`
	ExpiresAt               time.Time `json:"expires_at"`
	IntervalSeconds         int       `json:"interval_seconds"`
	Server                  string    `json:"server"`
}

// authenticatedRecord is the "authenticated" NDJSON record.
type authenticatedRecord struct {
	Type      string         `json:"type"`
	Account   accountPayload `json:"account"`
	Session   sessionPayload `json:"session"`
	Server    string         `json:"server"`
	Storage   storagePayload `json:"storage"`
	Method    string         `json:"method"`
	ExpiresAt time.Time      `json:"access_expires_at"`
}

// accountPayload is the account snapshot embedded in records.
type accountPayload struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Nick        string `json:"nick"`
	PerkLevel   int    `json:"perk_level,omitempty"`
	Level       int    `json:"level,omitempty"`
	Experience  int    `json:"experience,omitempty"`
	JoinedAt    string `json:"joined_at,omitempty"`
	Supersuser  bool   `json:"is_superuser,omitempty"`
	ActivatedAt string `json:"activated_at,omitempty"`
}

// sessionPayload describes the server-side session.
type sessionPayload struct {
	ID        string `json:"id,omitempty"`
	TokenType string `json:"token_type,omitempty"`
}

// storagePayload tells scripts where the refresh token lives.
type storagePayload struct {
	Backend string `json:"backend"`
	Path    string `json:"path"`
}

// loginDevice runs the RFC 8628 device flow headlessly.
func (a *app) loginDevice(ctx context.Context) error {
	code, err := a.client.RequestDeviceCode(ctx)
	if err != nil {
		return err
	}

	expiresIn := code.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 600
	}
	rec := authzRecord{
		Type:                    ui.RecordAuthorization,
		UserCode:                code.UserCode,
		VerificationURI:         code.VerificationURI,
		VerificationURIComplete: code.VerificationURIComplete,
		ExpiresAt:               time.Now().Add(time.Duration(expiresIn) * time.Second),
		IntervalSeconds:         code.Interval,
		Server:                  a.cfg.Server,
	}

	if a.out.JSON() {
		if err := a.out.Emit(rec); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.stderr, "To sign in, open %s and enter the code %s\n",
			code.VerificationURI, code.UserCode)
		if code.VerificationURIComplete != "" {
			fmt.Fprintf(a.stderr, "Or open %s directly.\n", code.VerificationURIComplete)
		}
		a.out.Textf("waiting for approval (expires in %s)…", countdown(time.Duration(expiresIn)*time.Second))
	}

	interval := a.pollInterval(code.Interval)
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}

		if time.Now().After(deadline) {
			return &api.DeviceError{Code: api.DeviceExpiredToken, Description: "device code expired"}
		}

		tok, err := a.client.PollDeviceToken(ctx, code.DeviceCode)
		if err != nil {
			switch {
			case api.IsDeviceCode(err, api.DeviceAuthorizationPending):
				continue
			case api.IsDeviceCode(err, api.DeviceSlowDown):
				interval += 5 * time.Second
				continue
			default:
				return err
			}
		}

		creds := &credentials.Credentials{
			Server:       a.cfg.Server,
			Method:       credentials.MethodDevice,
			ClientID:     a.cfg.ClientID,
			DeviceID:     a.device.ID,
			RefreshToken: tok.RefreshToken,
			ObtainedAt:   time.Now(),
		}
		return a.finishHeadlessLogin(ctx, creds, tok.AccessToken, tok.ExpiresIn)
	}
}

// pollInterval resolves the device poll interval.
func (a *app) pollInterval(serverSeconds int) time.Duration {
	if serverSeconds <= 0 {
		serverSeconds = 5
	}
	return time.Duration(serverSeconds) * time.Second
}

// loginPassword runs the challenge flow headlessly.
func (a *app) loginPassword(ctx context.Context) error {
	account, err := a.readLine("account (username or email): ")
	if err != nil {
		return err
	}

	ch, err := a.client.CreateChallenge(ctx, account)
	if err != nil {
		return err
	}

	for {
		factors, err := a.client.Factors(ctx, ch.ID)
		if err != nil {
			return err
		}
		usable := filterUsable(factors, ch.BlacklistFactors)
		if len(usable) == 0 {
			if ch.StepRemain == 0 {
				break
			}
			return &api.APIError{
				Code:    "NO_AUTH_FACTORS",
				Message: "no factor left that Termian can complete — use device login instead",
			}
		}

		factor, err := a.chooseFactor(usable)
		if err != nil {
			return err
		}

		if factor.Type == api.FactorEmailCode {
			if err := a.client.RequestFactorCode(ctx, ch.ID, factor.ID); err != nil {
				return err
			}
			a.out.Textf("a code was emailed to the account address")
		}

		prompt := fmt.Sprintf("%s: ", factor.Label())
		secret, err := a.readSecret(prompt)
		if err != nil {
			return err
		}
		// A pasted trailing newline is trimmed by readSecret; mask never prints.

		ch, err = a.client.SubmitFactor(ctx, ch.ID, factor.ID, secret)
		if err != nil {
			if api.IsCode(err, "AUTH_INVALID_PASSWORD") {
				a.out.Progressf("that was not accepted; try again")
				continue
			}
			return err
		}
		if ch.StepRemain <= 0 {
			break
		}
		a.out.Progressf("%d factor(s) left", ch.StepRemain)
	}

	pair, err := a.client.ExchangeChallenge(ctx, ch.ID)
	if err != nil {
		return err
	}

	creds := &credentials.Credentials{
		Server:           a.cfg.Server,
		Method:           credentials.MethodChallenge,
		DeviceID:         a.device.ID,
		RefreshToken:     pair.RefreshToken,
		RefreshExpiresAt: expiryAfter(pair.RefreshExpiresIn),
		ObtainedAt:       time.Now(),
	}
	return a.finishHeadlessLogin(ctx, creds, pair.Token, pair.ExpiresIn)
}

// chooseFactor prompts for a factor when the account has more than one.
func (a *app) chooseFactor(usable []api.Factor) (api.Factor, error) {
	if len(usable) == 1 {
		return usable[0], nil
	}

	labels := make([]string, 0, len(usable))
	for i, f := range usable {
		labels = append(labels, fmt.Sprintf("%d) %s", i+1, f.Label()))
	}
	fmt.Fprintf(a.stderr, "choose a factor: %s\n", strings.Join(labels, "  "))

	choice, err := a.readLine("factor: ")
	if err != nil {
		return api.Factor{}, err
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(choice))
	if convErr != nil || n < 1 || n > len(usable) {
		return api.Factor{}, fmt.Errorf("invalid factor choice %q", choice)
	}
	return usable[n-1], nil
}

// filterUsable keeps factors a terminal can complete, minus consumed ones.
func filterUsable(factors []api.Factor, blacklist []string) []api.Factor {
	blocked := make(map[string]bool, len(blacklist))
	for _, id := range blacklist {
		blocked[id] = true
	}
	out := make([]api.Factor, 0, len(factors))
	for _, f := range factors {
		if blocked[f.ID] || !f.Usable() {
			continue
		}
		out = append(out, f)
	}
	return out
}

// finishHeadlessLogin persists credentials, loads the account and reports it.
func (a *app) finishHeadlessLogin(ctx context.Context, creds *credentials.Credentials, access string, expiresIn int64) error {
	if err := a.store.Save(creds); err != nil {
		return fmt.Errorf("store credentials: %w", err)
	}
	a.creds = creds
	a.tokens.Seed(creds, access, expiresIn)

	id, acc, err := a.loadAccount(ctx)
	if err != nil {
		return err
	}

	// Cache the account snapshot so `whoami` can answer without a round trip.
	creds.AccountID = id.ID
	creds.AccountName = id.Name
	creds.Nick = id.Nick
	if err := a.store.Save(creds); err != nil {
		return fmt.Errorf("store credentials: %w", err)
	}
	a.tokens.Seed(creds, access, expiresIn)

	record := authenticatedRecord{
		Type:      ui.RecordAuthenticated,
		Account:   accountFrom(id, acc),
		Session:   sessionPayload{ID: id.SessionID, TokenType: id.TokenType},
		Server:    a.cfg.Server,
		Storage:   a.storage(),
		Method:    creds.Method,
		ExpiresAt: time.Now().Add(time.Duration(expiresIn) * time.Second),
	}
	if a.out.JSON() {
		return a.out.Emit(record)
	}

	fmt.Fprintf(a.stdout, "signed in as %s (@%s)\n", record.Account.Nick, record.Account.Name)
	a.out.Progressf("session %s · stored in %s", record.Session.ID, record.Storage.Backend)
	return nil
}

// loadAccount fetches the identity and account for the current session.
func (a *app) loadAccount(ctx context.Context) (api.Identity, api.Account, error) {
	if _, err := a.tokens.Token(ctx); err != nil {
		return api.Identity{}, api.Account{}, err
	}
	id, err := a.client.Identity(ctx)
	if err != nil {
		return api.Identity{}, api.Account{}, err
	}
	acc, err := a.client.Account(ctx)
	if err != nil {
		return id, api.Account{}, err
	}
	return id, acc, nil
}

// storage describes where credentials live.
func (a *app) storage() storagePayload {
	backend := string(credentials.BackendKeyring)
	path := "system keyring (termian)"
	if a.store != nil {
		backend = string(a.store.Backend)
		path = a.store.Describe(a.cfg.Server)
	}
	return storagePayload{Backend: backend, Path: path}
}

// accountFrom merges the identity and account snapshots into one payload.
func accountFrom(id api.Identity, acc api.Account) accountPayload {
	out := accountPayload{
		ID:         id.ID,
		Name:       id.Name,
		Nick:       id.Nick,
		PerkLevel:  acc.PerkLevel,
		Supersuser: id.IsSuperuser,
	}
	if out.ID == "" {
		out.ID = acc.ID
	}
	if out.Name == "" {
		out.Name = acc.Name
	}
	if out.Nick == "" {
		out.Nick = acc.Nick
	}
	if id.ActivatedAt != nil {
		out.ActivatedAt = id.ActivatedAt.Format(time.RFC3339)
	}
	if acc.Profile != nil {
		out.Level = acc.Profile.Level
		out.Experience = acc.Profile.Experience
		if acc.Profile.CreatedAt != nil {
			out.JoinedAt = acc.Profile.CreatedAt.Format("2006-01-02")
		}
	}
	return out
}

// expiryAfter converts an OAuth lifetime into an absolute deadline.
func expiryAfter(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(seconds) * time.Second)
}

// countdown formats a remaining duration as mm:ss.
func countdown(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}
