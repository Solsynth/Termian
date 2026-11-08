// Package cli wires the Termian commands: the interactive TUI, the scriptable
// login flows and the session utilities.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/config"
	"src.solsynth.dev/sosys/termian/internal/credentials"
	"src.solsynth.dev/sosys/termian/internal/deviceid"
	"src.solsynth.dev/sosys/termian/internal/tui"
	"src.solsynth.dev/sosys/termian/internal/ui"
	"src.solsynth.dev/sosys/termian/internal/version"
)

// exitSignalled is returned when the user interrupts a command.
const exitSignalled = 130

// usageError marks invalid invocations, which exit 2 rather than 1.
type usageError struct{ msg string }

// Error implements error.
func (e *usageError) Error() string { return e.msg }

// options collects every flag value.
type options struct {
	server     string
	clientID   string
	output     string
	store      string
	configPath string
	deviceName string
	timeout    time.Duration
	verbose    bool

	noTUI       bool
	device      bool
	password    bool
	attachments string
	chatHistory int
}

// app is the per-invocation runtime shared by all commands.
type app struct {
	opts   options
	cfg    config.Config
	out    *ui.Renderer
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	device deviceid.Identity
	store  *credentials.Store
	client *api.Client
	tokens *api.TokenSource
	creds  *credentials.Credentials

	inputLines *bufio.Reader
	// parsed flips once cobra accepts the command line, so a failure before
	// that point is a usage error rather than a runtime one.
	parsed bool
}

// Execute runs the command tree and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := &app{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
	return a.run(ctx, os.Args[1:])
}

// run executes the command tree with args and reports the process exit code,
// writing exactly one error record for a failure.
func (a *app) run(ctx context.Context, args []string) int {
	root := a.rootCommand()
	root.SetArgs(args)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return ui.ExitOK
	}
	if errors.Is(err, context.Canceled) {
		return exitSignalled
	}
	if a.out == nil {
		a.out = ui.New(a.stdout, a.stderr, "text")
	}
	var usage *usageError
	if !a.parsed || errors.As(err, &usage) {
		// Bad flag, bad argument, unknown command or conflicting flags.
		return ui.Fail(a.out, &ui.UsageError{Message: err.Error()})
	}
	return ui.Fail(a.out, err)
}

// rootCommand builds the command tree; the root command itself logs in.
func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "termian",
		Short: "Termian — the Solian terminal client",
		Long: "Termian signs in to Solian from the terminal.\n\n" +
			"Run without a subcommand to open the interactive client; use --no-tui or\n" +
			"--output json to drive the same flows from a script.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PersistentPreRun: func(*cobra.Command, []string) {
			a.parsed = true
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runLogin(cmd)
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&a.opts.server, "server", "", "Stargate API base (default https://api.solian.app)")
	pf.StringVar(&a.opts.clientID, "client-id", "", "OAuth client id used by the device flow (default solian)")
	pf.StringVar(&a.opts.output, "output", "", "output format: text or json")
	pf.StringVar(&a.opts.store, "store", "", "credential storage: auto, keyring or file")
	pf.StringVar(&a.opts.configPath, "config", "", "path to config.toml")
	pf.StringVar(&a.opts.deviceName, "device-name", "", "device name reported to the server")
	pf.DurationVar(&a.opts.timeout, "timeout", api.DefaultTimeout, "per-request timeout")
	pf.BoolVar(&a.opts.verbose, "verbose", false, "log storage and transport details to stderr")
	pf.BoolVar(&a.opts.noTUI, "no-tui", false, "never start the interactive UI")
	pf.BoolVar(&a.opts.device, "device", false, "device-flow login (default)")
	pf.BoolVar(&a.opts.password, "password", false, "password/TOTP challenge login")
	pf.StringVar(&a.opts.attachments, "attachments", "", "attachment handling: browser, preview or off")
	pf.IntVar(&a.opts.chatHistory, "chat-history", 0, "messages kept per chat room")

	root.AddCommand(
		a.loginCommand(),
		a.logoutCommand(),
		a.whoamiCommand(),
		a.refreshCommand(),
		a.versionCommand(),
		a.feedCommand(),
		a.showPostCommand(),
		a.publishCommand(),
		a.replyCommand(),
		a.saveCommand(),
		a.reactCommand(),
		a.bookmarksCommand(),
		a.publishersCommand(),
		a.chatsCommand(),
		a.chatCommand(),
	)

	return root
}

// loginCommand is the explicit login verb; it shares the root flow.
func (a *app) loginCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Sign in to Solian",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runLogin(cmd)
		},
	}
}

// versionCommand prints the build version.
func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Termian version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.prepare(); err != nil {
				return err
			}
			if a.out.JSON() {
				return a.out.Emit(map[string]any{
					"type":    "version",
					"version": version.Version,
				})
			}
			fmt.Fprintf(a.stdout, "termian %s\n", version.Version)
			return nil
		},
	}
}

// prepare resolves configuration, storage, device identity and the API client.
func (a *app) prepare() error {
	if a.client != nil {
		return nil
	}

	cfg, err := config.Load(config.Overrides{
		Server:      a.opts.server,
		ClientID:    a.opts.clientID,
		Store:       a.opts.store,
		Output:      a.opts.output,
		Attachments: a.opts.attachments,
		ChatHistory: a.opts.chatHistory,
		Path:        a.opts.configPath,
	})
	if err != nil {
		return err
	}
	a.out = ui.New(a.stdout, a.stderr, cfg.Output)

	store, err := credentials.Open(cfg.Store, cfg.StateDir)
	if err != nil {
		return err
	}
	a.store = store
	// The notice is human-facing; JSON mode keeps stderr reserved for the
	// single error record so scripts can parse it.
	if notice := store.Notice(mustNormalized(cfg.Server)); notice != "" && !a.out.JSON() {
		fmt.Fprintln(a.stderr, notice)
	}

	device, err := deviceid.Load(cfg.StateDir)
	if err != nil {
		return err
	}
	if a.opts.deviceName != "" {
		device.Name = a.opts.deviceName
	}
	a.device = device

	server, err := api.NormalizeBase(cfg.Server)
	if err != nil {
		return err
	}
	// The server keys credentials, so only the normalized form is stored.
	cfg.Server = server.String()
	a.cfg = cfg

	client, err := api.New(cfg.Server, cfg.ClientID, cfg.Scope, device, version.Version)
	if err != nil {
		return err
	}
	if a.opts.timeout > 0 {
		client.HTTP.Timeout = a.opts.timeout
	}
	a.client = client

	creds, err := store.Load(cfg.Server)
	if err != nil {
		return err
	}
	a.creds = creds
	a.tokens = api.NewTokenSource(client, store, creds)

	a.verbosef("server=%s client_id=%s store=%s output=%s device=%s",
		cfg.Server, cfg.ClientID, store.Backend, cfg.Output, device.ID)
	a.verbosef("attachments=%s chat_history=%d", cfg.Attachments, cfg.ChatHistory)
	a.verbosef("storage: %s", store.Describe(cfg.Server))
	if creds != nil {
		a.verbosef("stored session: method=%s account=%s obtained=%s",
			creds.Method, creds.AccountName, creds.ObtainedAt.Format(time.RFC3339))
	} else {
		a.verbosef("no stored session for %s", cfg.Server)
	}
	return nil
}

// mustNormalized returns the normalized server string, falling back to the raw
// value when it cannot be parsed.
func mustNormalized(raw string) string {
	if normalized, err := api.NormalizeBase(raw); err == nil {
		return normalized.String()
	}
	return raw
}

// tuiAllowed reports whether the interactive client may start: not disabled by a
// flag, text output, and stdout attached to a terminal.
func (a *app) tuiAllowed() bool {
	if a.opts.noTUI {
		return false
	}
	if a.cfg.Output != string(ui.ModeText) {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// runLogin dispatches between the interactive client and the headless flows.
func (a *app) runLogin(cmd *cobra.Command) error {
	if a.opts.device && a.opts.password {
		return &usageError{msg: "choose either --device or --password"}
	}
	if err := a.prepare(); err != nil {
		return err
	}

	if a.tuiAllowed() {
		return a.runTUI(cmd.Context())
	}
	if a.opts.password {
		return a.loginPassword(cmd.Context())
	}
	return a.loginDevice(cmd.Context())
}

// runTUI starts the interactive client.
func (a *app) runTUI(ctx context.Context) error {
	return tui.Run(tui.Deps{
		Ctx:         ctx,
		API:         a.client,
		Content:     a.client,
		Tokens:      a.tokens,
		Store:       a.store,
		Server:      a.cfg.Server,
		ClientID:    a.cfg.ClientID,
		DeviceID:    a.device.ID,
		Version:     version.Version,
		Attachments: a.cfg.Attachments,
		ChatHistory: a.cfg.ChatHistory,
	})
}

// lineReader returns the buffered stdin reader.
func (a *app) lineReader() *bufio.Reader {
	if a.inputLines == nil {
		a.inputLines = bufio.NewReader(a.stdin)
	}
	return a.inputLines
}

// prompt writes an input prompt, but only in text mode: JSON mode keeps stderr
// free of anything but records.
func (a *app) prompt(text string) {
	if text == "" || (a.out != nil && a.out.JSON()) {
		return
	}
	fmt.Fprint(a.stderr, text)
}

// readLine reads one trimmed line from stdin.
func (a *app) readLine(prompt string) (string, error) {
	a.prompt(prompt)
	line, err := a.lineReader().ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read input: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		if errors.Is(err, io.EOF) {
			return "", errors.New("unexpected end of input")
		}
		return "", errors.New("empty input")
	}
	return line, nil
}

// readSecret reads a secret without echo on a terminal and as a plain line
// otherwise.
func (a *app) readSecret(prompt string) (string, error) {
	a.prompt(prompt)
	fd := int(os.Stdin.Fd())
	if a.stdin == os.Stdin && term.IsTerminal(fd) {
		buf, err := term.ReadPassword(fd)
		fmt.Fprintln(a.stderr)
		if err != nil {
			return "", fmt.Errorf("read secret: %w", err)
		}
		return string(buf), nil
	}
	line, err := a.lineReader().ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read secret: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("got an empty secret")
	}
	return line, nil
}

// verbosef logs diagnostics when --verbose is set.
func (a *app) verbosef(format string, args ...any) {
	if !a.opts.verbose {
		return
	}
	fmt.Fprintf(a.stderr, format+"\n", args...)
}
