package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/credentials"
)

// passwordStage is the sub-step of the challenge pane.
type passwordStage int

const (
	// stageAccount asks for the account name.
	stageAccount passwordStage = iota
	// stageFactor lists the usable factors.
	stageFactor
	// stageSecret asks for the factor secret.
	stageSecret
	// stageBusy is a transient state while a request is in flight.
	stageBusy
)

// passwordModel is the challenge-flow pane state.
type passwordModel struct {
	Stage   passwordStage
	Account textinput.Model
	Secret  textinput.Model

	Challenge api.Challenge
	Factors   []api.Factor
	// Selected indexes the visible (unused) factor list.
	Selected int
	// chosen is the factor currently being answered.
	chosen *api.Factor
	// used tracks factor IDs already consumed by this challenge.
	used map[string]bool
	// sentCode marks that an out-of-band code was requested for chosen.
	sentCode bool

	width int
}

// newPasswordModel builds an idle challenge pane.
func newPasswordModel(styles Styles) passwordModel {
	account := textinput.New()
	account.Placeholder = "username or email"
	account.Prompt = ""
	account.SetWidth(PasswordCardWidth - 10)
	account.CharLimit = 128

	secret := textinput.New()
	secret.Prompt = ""
	secret.SetWidth(PasswordCardWidth - 10)
	secret.CharLimit = 128

	m := passwordModel{
		Stage:   stageAccount,
		Account: account,
		Secret:  secret,
		width:   PasswordCardWidth,
		used:    map[string]bool{},
	}
	m.applyStyles(styles)
	return m
}

// applyStyles re-tints the text inputs for the current palette.
func (p *passwordModel) applyStyles(styles Styles) {
	p.Account.SetStyles(inputStyles(styles))
	p.Secret.SetStyles(inputStyles(styles))
}

// inputStyles maps the palette onto a text input.
func inputStyles(styles Styles) textinput.Styles {
	s := textinput.DefaultDarkStyles()
	s.Focused.Prompt = styles.Accent
	s.Focused.Text = styles.Foreground
	s.Focused.Placeholder = styles.Muted
	s.Focused.Suggestion = styles.Muted
	s.Blurred.Prompt = styles.Muted
	s.Blurred.Text = styles.Foreground
	s.Blurred.Placeholder = styles.Muted
	s.Blurred.Suggestion = styles.Muted
	s.Cursor.Color = styles.Accent.GetForeground()
	return s
}

// setWidth records the card width.
func (p *passwordModel) setWidth(w int) {
	p.width = w
	p.Account.SetWidth(max(w-10, 10))
	p.Secret.SetWidth(max(w-10, 10))
}

// reset returns the pane to the account prompt.
func (p *passwordModel) reset() {
	p.Stage = stageAccount
	p.Challenge = api.Challenge{}
	p.Factors = nil
	p.Selected = 0
	p.chosen = nil
	p.sentCode = false
	p.used = map[string]bool{}
	p.Secret.Reset()
	p.Secret.Blur()
	p.Account.Focus()
}

// focusSecret prepares the secret input for the chosen factor.
func (p *passwordModel) focusSecret() {
	p.Stage = stageSecret
	p.Secret.Reset()
	if p.chosen != nil && p.chosen.SecretIsOpaque() {
		p.Secret.EchoMode = textinput.EchoPassword
		p.Secret.EchoCharacter = '•'
		p.Secret.CharLimit = 0
	} else {
		// Email and authenticator codes are six digits.
		p.Secret.EchoMode = textinput.EchoNormal
		p.Secret.CharLimit = 6
	}
	p.Secret.Focus()
	p.Account.Blur()
}

// visibleFactors returns the factors not yet consumed by this challenge.
func (p *passwordModel) visibleFactors() []api.Factor {
	out := make([]api.Factor, 0, len(p.Factors))
	for _, f := range p.Factors {
		if p.used[f.ID] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// selectedFactor returns the highlighted visible factor, if any.
func (p *passwordModel) selectedFactor() *api.Factor {
	visible := p.visibleFactors()
	if p.Selected < 0 || p.Selected >= len(visible) {
		return nil
	}
	factor := visible[p.Selected]
	return &factor
}

// passwordView renders the challenge card.
func (m *Model) passwordView() string {
	p := &m.Password
	var lines []string

	lines = append(lines, m.Styles.Title.Render("Termian"))
	lines = append(lines, m.Styles.Subtitle.Render("Password login"))
	lines = append(lines, "")

	visible := p.visibleFactors()
	switch p.Stage {
	case stageAccount, stageBusy:
		if p.Stage == stageBusy {
			lines = append(lines, m.Spinner.View()+" "+m.Styles.Muted.Render("working…"))
			break
		}
		lines = append(lines, m.Styles.Label.Render("account"))
		lines = append(lines, p.Account.View())
	case stageFactor:
		lines = append(lines, m.Styles.Label.Render("choose a factor"))
		for i, f := range visible {
			if i == p.Selected {
				lines = append(lines, m.Styles.Selected.Render("▸ "+f.Label()))
			} else {
				lines = append(lines, m.Styles.Foreground.Render("  "+f.Label()))
			}
		}
		if p.Challenge.ID != "" && len(visible) == 0 {
			lines = append(lines, "", m.Styles.Error.Render(
				"this account needs a passkey, NFC or another device to approve — use device login instead"))
		}
	case stageSecret:
		name := "secret"
		if p.chosen != nil {
			name = p.chosen.Label()
		}
		lines = append(lines, m.Styles.Label.Render(name))
		lines = append(lines, p.Secret.View())
		if p.sentCode {
			lines = append(lines, "", m.Styles.Notice.Render("a code was sent"))
		}
	}

	lines = append(lines, "")
	lines = append(lines, m.Styles.Hint.Render("enter submit · ↑↓ select · esc device login · ctrl+c quit"))

	card := m.Styles.Card.Width(p.width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	return card
}

// updatePasswordKey handles keys on the challenge pane.
func (m *Model) updatePasswordKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.Password

	switch key.String() {
	case "esc":
		m.showScreen(ScreenLogin)
		p.reset()
		return m, nil
	case "enter":
		return m.passwordSubmit()
	case "up", "k":
		if p.Stage == stageFactor && p.Selected > 0 {
			p.Selected--
			return m, nil
		}
	case "down", "j":
		if p.Stage == stageFactor && p.Selected < len(p.visibleFactors())-1 {
			p.Selected++
			return m, nil
		}
	}

	var cmd tea.Cmd
	switch p.Stage {
	case stageAccount:
		p.Account, cmd = p.Account.Update(key)
	case stageSecret:
		p.Secret, cmd = p.Secret.Update(key)
	}
	return m, cmd
}

// passwordSubmit advances the current stage.
func (m *Model) passwordSubmit() (tea.Model, tea.Cmd) {
	p := &m.Password
	switch p.Stage {
	case stageAccount:
		account := strings.TrimSpace(p.Account.Value())
		if account == "" {
			m.setBanner("enter a username or email")
			return m, nil
		}
		m.clearBanner()
		p.Stage = stageBusy
		return m, m.createChallengeCmd(account)

	case stageFactor:
		factor := p.selectedFactor()
		if factor == nil {
			m.setBanner("no usable factor to submit")
			return m, nil
		}
		p.chosen = factor
		p.sentCode = false
		m.clearBanner()
		p.focusSecret()
		if factor.Type == api.FactorEmailCode {
			return m, m.requestFactorCodeCmd(p.Challenge.ID, factor.ID)
		}
		return m, nil

	case stageSecret:
		factor := p.chosen
		if factor == nil {
			p.Stage = stageFactor
			return m, nil
		}
		secret := p.Secret.Value()
		if secret == "" {
			m.setBanner("enter the secret")
			return m, nil
		}
		m.clearBanner()
		p.Stage = stageBusy
		return m, m.submitFactorCmd(p.Challenge.ID, factor.ID, secret)
	}
	return m, nil
}

// createChallengeCmd opens a challenge for account and loads its factors.
func (m *Model) createChallengeCmd(account string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		ch, err := m.Deps.API.CreateChallenge(ctx, account)
		if err != nil {
			return challengeMsg{err: err}
		}
		factors, err := m.Deps.API.Factors(ctx, ch.ID)
		return challengeMsg{ch: ch, factors: factors, err: err}
	})
}

// requestFactorCodeCmd triggers an out-of-band code for a factor.
func (m *Model) requestFactorCodeCmd(challengeID, factorID string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		err := m.Deps.API.RequestFactorCode(ctx, challengeID, factorID)
		return factorCodeMsg{err: err}
	})
}

// submitFactorCmd verifies one factor.
func (m *Model) submitFactorCmd(challengeID, factorID, secret string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		ch, err := m.Deps.API.SubmitFactor(ctx, challengeID, factorID, secret)
		return stepResultMsg{ch: ch, err: err}
	})
}

// exchangeChallengeCmd trades a satisfied challenge for tokens.
func (m *Model) exchangeChallengeCmd(challengeID string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		pair, err := m.Deps.API.ExchangeChallenge(ctx, challengeID)
		if err != nil {
			return exchangedMsg{err: err}
		}
		creds := credentials.Credentials{
			Server:           m.Deps.Server,
			Method:           credentials.MethodChallenge,
			DeviceID:         m.Deps.DeviceID,
			RefreshToken:     pair.RefreshToken,
			RefreshExpiresAt: expiryAfter(pair.RefreshExpiresIn),
		}
		return exchangedMsg{pair: pair, creds: creds}
	})
}

// updatePasswordMsg handles asynchronous results on the challenge pane.
func (m *Model) updatePasswordMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	p := &m.Password
	switch msg := msg.(type) {
	case challengeMsg:
		if msg.err != nil {
			p.Stage = stageAccount
			p.Account.Focus()
			m.setBanner(msg.err.Error())
			return m, nil
		}
		p.Challenge = msg.ch
		p.Factors = filterFactors(msg.factors)
		p.Selected = 0
		p.used = map[string]bool{}
		for _, id := range msg.ch.BlacklistFactors {
			p.used[id] = true
		}
		p.Stage = stageFactor
		m.clearBanner()
		return m, m.scheduleChallengePoll()

	case challengeTickMsg:
		return m.handleChallengeTick(msg)

	case factorsMsg:
		if msg.err != nil {
			m.setBanner(msg.err.Error())
			return m, nil
		}
		p.Factors = filterFactors(msg.factors)
		p.Selected = 0
		return m, nil

	case factorCodeMsg:
		if msg.err != nil {
			p.Stage = stageSecret
			m.setBanner("could not send the code: " + msg.err.Error())
			return m, nil
		}
		p.Stage = stageSecret
		p.sentCode = true
		p.Secret.Focus()
		return m, nil

	case stepResultMsg:
		return m.handleStepResult(msg)

	case exchangedMsg:
		if msg.err != nil {
			p.Stage = stageFactor
			m.setBanner(msg.err.Error())
			return m, nil
		}
		if m.Deps.Tokens != nil {
			m.Deps.Tokens.Seed(&msg.creds, msg.pair.Token, msg.pair.ExpiresIn)
		}
		p.reset()
		return m, m.finishLoginCmd(msg.creds, msg.pair.Token, msg.pair.ExpiresIn)
	}
	return m, nil
}

// handleStepResult advances the challenge after one factor submission.
func (m *Model) handleStepResult(msg stepResultMsg) (tea.Model, tea.Cmd) {
	p := &m.Password
	if msg.err != nil {
		if api.IsCode(msg.err, "AUTH_INVALID_PASSWORD") {
			m.setBanner("wrong password/code; attempts are limited")
		} else {
			m.setBanner(msg.err.Error())
		}
		p.focusSecret()
		return m, nil
	}

	p.Challenge = msg.ch
	if p.chosen != nil {
		p.used[p.chosen.ID] = true
	}
	for _, id := range msg.ch.BlacklistFactors {
		p.used[id] = true
	}
	p.chosen = nil
	p.sentCode = false
	p.Selected = 0

	if msg.ch.StepRemain <= 0 {
		return m, m.exchangeChallengeCmd(msg.ch.ID)
	}
	p.Stage = stageFactor
	m.clearBanner()
	return m, m.scheduleChallengePoll()
}

// handleChallengeTick advances the cross-device poll.
func (m *Model) handleChallengeTick(msg challengeTickMsg) (tea.Model, tea.Cmd) {
	p := &m.Password
	if p.Challenge.ID == "" {
		return m, nil
	}
	if msg.err != nil {
		// Transient failures must not kill the flow.
		return m, m.scheduleChallengePoll()
	}
	if msg.due {
		return m, m.fetchChallengeCmd(p.Challenge.ID)
	}

	previous := p.Challenge
	p.Challenge = msg.ch
	switch {
	case msg.ch.DeclinedAt != nil && previous.DeclinedAt == nil:
		m.setBanner("sign-in was declined on another device")
		p.Selected = 0
		p.Stage = stageFactor
		return m, m.relistFactorsCmd(msg.ch.ID)
	case msg.ch.StepRemain <= 0:
		return m, m.exchangeChallengeCmd(msg.ch.ID)
	case msg.ch.StepRemain != previous.StepRemain:
		p.selectedSync(msg.ch.BlacklistFactors)
		if p.Stage != stageSecret {
			p.Stage = stageFactor
		}
		return m, m.relistFactorsCmd(msg.ch.ID)
	}
	return m, m.scheduleChallengePoll()
}

// selectedSync hides factors the server has already consumed.
func (p *passwordModel) selectedSync(blacklist []string) {
	for _, id := range blacklist {
		p.used[id] = true
	}
	if p.Selected >= len(p.visibleFactors()) {
		p.Selected = max(len(p.visibleFactors())-1, 0)
	}
}

// relistFactorsCmd re-reads the factor list for a challenge.
func (m *Model) relistFactorsCmd(challengeID string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		factors, err := m.Deps.API.Factors(ctx, challengeID)
		if err != nil {
			return factorsMsg{err: err}
		}
		return factorsMsg{factors: factors}
	})
}

// filterFactors keeps only the factors a terminal can complete.
func filterFactors(factors []api.Factor) []api.Factor {
	out := make([]api.Factor, 0, len(factors))
	for _, f := range factors {
		if f.Usable() {
			out = append(out, f)
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

// String implements fmt.Stringer for debug output.
func (p passwordModel) String() string {
	return fmt.Sprintf("password{stage=%d challenge=%s remain=%d factors=%d}",
		p.Stage, strings.TrimSpace(p.Challenge.ID), p.Challenge.StepRemain, len(p.Factors))
}
