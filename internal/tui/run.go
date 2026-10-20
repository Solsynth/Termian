package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// startupTimeout bounds the credential check that decides the initial screen.
const startupTimeout = 15 * time.Second

// Run starts the TUI and blocks until it exits.
//
// The initial screen is the session shell when stored credentials still refresh,
// and the device-flow login pane otherwise (in which case dead credentials are
// dropped first).
func Run(deps Deps) error {
	if deps.Ctx == nil {
		return errors.New("tui: missing program context")
	}

	screen := ScreenLogin
	if deps.Tokens != nil && deps.Tokens.Credentials() != nil {
		ctx, cancel := context.WithTimeout(deps.Ctx, startupTimeout)
		_, err := deps.Tokens.Token(ctx)
		cancel()
		if err == nil {
			screen = ScreenHome
		} else if errors.Is(err, api.ErrSessionExpired) {
			screen = ScreenLogin
		} else {
			// Transport problems must not throw the session away: keep the
			// session shell and let the home screen report the failure.
			screen = ScreenHome
		}
	}

	model := NewModelWithScreen(deps, screen)
	_, err := tea.NewProgram(model, tea.WithContext(deps.Ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	return err
}
