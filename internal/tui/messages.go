package tui

import (
	"time"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/credentials"
)

// deviceCodeMsg carries the device authorization response (or its error).
type deviceCodeMsg struct {
	code api.DeviceCode
	err  error
}

// pollDueMsg fires when the next device token poll is due.
type pollDueMsg struct {
	at time.Time
}

// deviceTokenMsg carries the result of one device token poll.
type deviceTokenMsg struct {
	tok api.OAuthToken
	err error
}

// challengeMsg carries a freshly created challenge and its factor list.
type challengeMsg struct {
	ch      api.Challenge
	factors []api.Factor
	err     error
}

// factorsMsg carries a refreshed factor list.
type factorsMsg struct {
	factors []api.Factor
	err     error
}

// factorCodeMsg reports the outcome of requesting an out-of-band code.
type factorCodeMsg struct {
	err error
}

// stepResultMsg carries the challenge state after one verified factor.
type stepResultMsg struct {
	ch  api.Challenge
	err error
}

// exchangedMsg carries the tokens issued for a satisfied challenge.
type exchangedMsg struct {
	pair  api.TokenPair
	creds credentials.Credentials
	err   error
}

// challengeTickMsg drives the 2 s cross-device challenge poll: due=true means
// "the timer fired, fetch the challenge", otherwise it carries a fetch result.
type challengeTickMsg struct {
	ch  api.Challenge
	err error
	due bool
}

// authedMsg carries the account snapshot fetched right after authentication.
type authedMsg struct {
	id  api.Identity
	acc api.Account
	err error
}

// refreshedMsg reports a completed access-token rotation.
type refreshedMsg struct {
	at  time.Time
	err error
}

// loggedOutMsg reports a completed logout.
type loggedOutMsg struct {
	err error
}

// browserDoneMsg reports the outcome of opening the verification URL.
type browserDoneMsg struct {
	err error
}

// homeTickMsg drives the session card countdown.
type homeTickMsg struct {
	at time.Time
}
