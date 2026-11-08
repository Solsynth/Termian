module src.solsynth.dev/sosys/termian/hosted

go 1.26.8

require (
	charm.land/bubbletea/v2 v2.1.0
	charm.land/ssh v0.4.3
	charm.land/wish/v2 v2.0.5
	github.com/spf13/cobra v1.10.2
	golang.org/x/crypto v0.57.0
	google.golang.org/grpc v1.85.0
	src.solsynth.dev/sosys/lighthouse/shared v0.2.6
	src.solsynth.dev/sosys/termian v0.0.0
)

require (
	charm.land/bubbles/v2 v2.2.1 // indirect
	charm.land/lipgloss/v2 v2.0.6 // indirect
	charm.land/log/v2 v2.0.1 // indirect
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/keygen v0.5.4 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20261008173134-6b8d4baf91b4 // indirect
	github.com/charmbracelet/x/ansi v0.11.9 // indirect
	github.com/charmbracelet/x/conpty v0.2.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/charmbracelet/x/xpty v0.1.4 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/go-logfmt/logfmt v0.6.1 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.30 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/pires/go-proxyproto v0.15.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/xo/terminfo v1.2.0 // indirect
	github.com/zalando/go-keyring v0.2.8 // indirect
	golang.org/x/exp v0.0.0-20260908205506-85c1c2202aba // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260817212433-ac3dfec99bb1 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

// The server shares the client's TUI, API and config packages. The replacement
// is local because both modules live in this repository.
replace src.solsynth.dev/sosys/termian => ..
