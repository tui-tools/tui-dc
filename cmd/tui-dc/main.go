// Command tui-dc administers a Samba Active Directory domain controller from
// the terminal.
//
// It is read-mostly. Opening it reads the domain — its name, its functional
// levels, the role this host plays, the accounts, the groups, the machine
// accounts, the domain's DNS zone and the state of replication — and shows all
// of it without changing anything. Every change is one `samba-tool` command,
// shown in full and confirmed before it runs.
//
// It does not provision, join or demote a domain. Those have no undo, they are
// rare, and their command lines are worth reading in a shell where they can be
// checked twice.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-dc/internal/samba"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/config"
	"github.com/tui-tools/tui-kit/theme"
)

// toolName is the binary name, which is also the configuration directory:
// /etc/tui-dc/config.toml and ~/.config/tui-dc/config.toml.
const toolName = "tui-dc"

// keyServer is the controller the read path asks. It defaults to this host,
// because the tool administers the controller it runs on.
const keyServer = "server"

// version is stamped by the release build (-ldflags "-X main.version=…").
var version = "dev"

// defaults declares the configuration keys the tool understands. Only these
// are read from the environment (TUI_DC_SERVER, …), so an unrelated variable
// can never leak into the configuration.
func defaults() map[string]string {
	return map[string]string{
		keyServer:       samba.DefaultServer,
		config.KeySudo:  "sudo -n",
		config.KeyTheme: "",
	}
}

// options holds the parsed command line.
type options struct {
	demo        bool
	check       bool
	report      bool
	server      string
	themePath   string
	sudo        string
	showVersion bool
	// sudoSet records whether -sudo was passed, so `--sudo ""` can disable
	// escalation instead of reading as "not given".
	sudoSet bool
}

// parseFlags defines and reads the command line.
func parseFlags(args []string, out *os.File) (options, error) {
	var opts options
	fs := flag.NewFlagSet(toolName, flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&opts.demo, "demo", false,
		"run against a sample domain, without touching anything")
	fs.BoolVar(&opts.check, "check", false,
		"run the read path once and print what it parsed as JSON, then exit "+
			"(no UI, no changes: safe to run anywhere, including in CI)")
	fs.BoolVar(&opts.report, "report", false, reportUsage)
	fs.StringVar(&opts.server, "server", "",
		"the domain controller to read (overrides the config file)")
	fs.StringVar(&opts.themePath, "theme", "",
		"path to an Omarchy-style colors.toml (overrides the config file)")
	fs.StringVar(&opts.sudo, "sudo", "",
		"privilege escalation prefix, e.g. \"sudo -n\" or \"\" to disable")
	fs.BoolVar(&opts.showVersion, "version", false, "print the version and exit")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(out, "tui-dc — a Samba Active Directory domain, "+
			"administered from the terminal\n\nUsage:\n  tui-dc [flags]\n\nFlags:\n")
		fs.PrintDefaults()
		_, _ = fmt.Fprintf(out, "\nConfiguration is read from %s, then %s, "+
			"then TUI_DC_* in the environment.\n",
			config.SystemPathFor(toolName), config.UserPathFor(toolName))
	}
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "sudo" {
			opts.sudoSet = true
		}
	})
	return opts, nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, toolName+":", err)
		os.Exit(1)
	}
}

// run wires the configuration, the backend and the Bubble Tea program. Every
// tool in the family has this function, and it is worth keeping it recognisable.
func run(args []string) error {
	opts, err := parseFlags(args, os.Stdout)
	if err != nil {
		// flag already printed the reason and the usage.
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if opts.showVersion {
		fmt.Println(toolName, version)
		return nil
	}

	cfg, err := config.Load(config.Options{Tool: toolName, Defaults: defaults()})
	if err != nil {
		return err
	}
	applyOverrides(&cfg, opts)

	// The configured theme is handed to the kit through the same variable the
	// user could set by hand, so precedence stays in one place. It is set
	// before the backend is built so --report can name the theme the UI would
	// have used even on a machine where no backend can be.
	if path := cfg.Theme(); path != "" {
		if err := os.Setenv("TUI_THEME", path); err != nil {
			return err
		}
	}

	// --report is the non-interactive path that must work everywhere. It reads
	// nothing privileged and it survives a machine with no Samba on it,
	// because "there is nothing here to drive" is one of the things a bug
	// report has to be able to say. So it comes before the backend is required.
	if opts.report {
		return runReport(cfg, opts, os.Stdout)
	}

	backendCompat := probeCompat(context.Background(), opts.demo)

	backend, err := pickBackend(cfg, opts, backendCompat)
	if err != nil {
		return err
	}

	if opts.check {
		return runCheck(backend, backendCompat, os.Stdout)
	}

	program := tea.NewProgram(newApp(backend, theme.New(), backendCompat),
		tea.WithAltScreen())
	_, err = program.Run()
	return err
}

// applyOverrides folds the command line into the configuration, which is the
// last and highest-precedence layer.
func applyOverrides(cfg *config.Config, opts options) {
	if opts.server != "" {
		cfg.Set(keyServer, opts.server)
	}
	if opts.themePath != "" {
		cfg.Set(config.KeyTheme, opts.themePath)
	}
	// An explicitly empty -sudo disables escalation, so the flag is applied
	// whenever it was passed, empty value included.
	if opts.sudoSet {
		cfg.Set(config.KeySudo, opts.sudo)
	}
}

// pickBackend returns the demo backend or the real one.
func pickBackend(cfg config.Config, opts options,
	backendCompat compat.Result) (directory.Backend, error) {
	if opts.demo {
		return samba.NewFake(), nil
	}
	return samba.NewReal(
		samba.Options{Server: cfg.String(keyServer, samba.DefaultServer)},
		cfg.SudoPrefix(), backendCompat.Caps())
}
