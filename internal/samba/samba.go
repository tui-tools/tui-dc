// Package samba is the domain-controller backend of tui-dc, and the only place
// in the repository that starts a process.
//
// One program is driven, through one runner: `samba-tool`. Everything the tool
// knows about the domain it learned from a subcommand of it —
//
//	--version                 whether there is a Samba here at all
//	testparm                  this host's own role, realm and DNS backend
//	domain info <server>      whether a controller answers, and which one
//	domain level show         the functional levels
//	user list / user show     the accounts, and one account in full
//	group list / listmembers  the groups, and one group's membership
//	computer list / show      the machine accounts
//	dns query                 the domain zone
//	drs showrepl              replication, per naming context
//
// and every change is one more subcommand of the same program, previewed in
// full before it runs.
//
// One rule holds everywhere: no password is ever an argument. samba-tool
// warns about that itself ("Using passwords on command line is insecure"),
// and it is right — a command line is visible in `ps` to every user on the
// machine — so account creation and password resets ask samba-tool for a
// random password and show what it printed, and provisioning omits
// `--adminpass` entirely so samba-tool generates the Administrator password
// itself and prints it exactly once.
//
// Provisioning is the one moment this backend drives a second program:
// `systemctl enable --now <unit>` is offered — previewed and confirmed like
// everything else — so the freshly created domain actually starts. Joining
// and demoting remain absent: both touch a trust relationship with another
// controller, and a terminal UI on one host is the wrong place to take them.
//
// This is the domain controller side of Samba. The file server side —
// smb.conf, shares, sessions, the password database — is tui-samba, and the
// two do not overlap.
package samba

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/runner"
)

// ErrNotAvailable reports that samba-tool is not installed.
var ErrNotAvailable = runner.ErrNotAvailable

// searchPaths are the locations a non-root PATH commonly omits. samba-tool is
// an administrative program and lives in an sbin directory on some
// distributions, so without these a machine that is a domain controller would
// report "not installed".
var searchPaths = []string{
	"/usr/bin/samba-tool",
	"/bin/samba-tool",
	"/usr/sbin/samba-tool",
	"/sbin/samba-tool",
	"/usr/local/bin/samba-tool",
}

// DefaultServer is the controller the read path asks. It is this host, because
// the tool administers the domain controller it runs on: a remote one is a
// different trust relationship and would need credentials this tool does not
// take.
const DefaultServer = "127.0.0.1"

// readTimeout bounds one samba-tool invocation. samba-tool is a Python program
// that opens a database and sometimes the network, so it is not fast; a
// controller that is mid-election can make it slower still, and the UI must
// not be held by it forever.
const readTimeout = 25 * time.Second

// provisionTimeout bounds `domain provision`, which builds a whole directory
// database and cannot be held to the budget of a read.
const provisionTimeout = 10 * time.Minute

// Options are the settings the tool passes down from its configuration.
type Options struct {
	// Server is what `samba-tool domain info` and `samba-tool dns` are
	// pointed at. It defaults to this host.
	Server string
}

// Real reads and changes the domain on this host. It satisfies
// directory.Backend.
type Real struct {
	run  *runner.Runner
	opts Options
	// caps gates what only exists on a new enough samba-tool. It comes from
	// the manifest, so no version number is written into this file.
	caps compat.Caps
	// notAvailable is why samba-tool could not be located, when it could not.
	// It already reads as a sentence to show a user, so it is passed on whole
	// rather than rewrapped.
	notAvailable error
	// zone is the domain's own DNS zone, learned on the last Load. It is kept
	// because a DNS command needs it and a reader should not have to retype
	// the realm already on screen.
	zone string

	// provisionRun is a second runner over the same samba-tool, with the
	// timeout provisioning needs. It exists so a ten-minute budget cannot
	// leak onto ordinary commands.
	provisionRun *runner.Runner
	// systemctl drives the one non-samba command this backend offers: the
	// previewed `systemctl enable --now` after a provision. Nil when
	// systemctl is not installed, and everything but that offer works
	// without it.
	systemctl *runner.Runner

	// loaded, isDC and reachable are what the last Load concluded, kept so
	// BuildProvision can refuse on a host that already serves a domain even
	// if a caller never looked at the model.
	loaded    bool
	isDC      bool
	reachable bool
}

// Available reports whether this machine has a samba-tool to drive.
func Available() bool { return runner.Available(directory.Bin, searchPaths...) }

// NewReal locates samba-tool and, when not running as root, validates the
// configured privilege prefix.
//
// Reads are privileged: samba-tool opens the directory database directly, and
// that database is readable only by root. A user who cannot escalate finds out
// here, at startup, rather than after the first empty screen.
//
// samba-tool itself is not required. A machine with no Samba on it still
// starts the tool and still answers --check, and what it says is "there is no
// samba-tool here" — which is the true answer for most machines, and a far
// better one than a refusal to start. What it cannot do on such a machine is
// build a command, and Build says so.
func NewReal(opts Options, sudoPrefix []string, caps compat.Caps) (*Real, error) {
	if opts.Server == "" {
		opts.Server = DefaultServer
	}
	real := &Real{opts: opts, caps: caps}
	r, err := runner.New(runner.Options{
		Bin:         directory.Bin,
		SearchPaths: searchPaths,
		SudoPrefix:  sudoPrefix,
		Timeout:     readTimeout,
		InstallHint: "it comes with the samba package (samba-common-bin on Debian)",
	})
	if err != nil {
		// Only a missing samba-tool is survivable. A privilege prefix that
		// does not exist is a configuration mistake, and a tool that carried
		// on past it would fail on the first change instead of at startup.
		if !errors.Is(err, runner.ErrNotAvailable) || Available() {
			return nil, err
		}
		real.notAvailable = err
		return real, nil
	}
	real.run = r

	// The provisioning runner is the same binary, the same escalation, a
	// longer leash. Building it here cannot fail: r just resolved the same
	// options.
	if p, err := runner.New(runner.Options{
		Bin:         directory.Bin,
		SearchPaths: searchPaths,
		SudoPrefix:  sudoPrefix,
		Timeout:     provisionTimeout,
		InstallHint: "it comes with the samba package (samba-common-bin on Debian)",
	}); err == nil {
		real.provisionRun = p
	}
	// systemctl is optional: without it the tool still provisions, and the
	// user starts the service themselves — the result screen says which one.
	if s, err := runner.New(runner.Options{
		Bin:         "systemctl",
		SearchPaths: []string{"/usr/bin/systemctl", "/bin/systemctl"},
		SudoPrefix:  sudoPrefix,
	}); err == nil {
		real.systemctl = s
	}
	return real, nil
}

// Name identifies the backend. The manifest declares one block, `samba`, for
// the whole suite: samba-tool ships with it and carries its version.
func (r *Real) Name() string { return "samba" }

// Describe is the one-line summary shown in the header.
func (r *Real) Describe() string {
	if r.run == nil {
		return "no samba-tool on this machine"
	}
	if r.run.Privileged() {
		return r.opts.Server + "  ·  via " + strings.Join(r.run.Privilege, " ")
	}
	return r.opts.Server
}

// Preview renders the exact command line Run will execute.
func (r *Real) Preview(cmd runner.Command) string {
	if r.run == nil {
		return strings.Join(cmd.Argv, " ")
	}
	return r.run.Preview(cmd)
}

// Run executes a previewed command, routed to the runner that owns its
// program: systemctl commands to the systemctl runner, a provision to the
// long-timeout runner, everything else to the ordinary one.
func (r *Real) Run(ctx context.Context, cmd runner.Command) (string, error) {
	if len(cmd.Argv) > 0 && cmd.Argv[0] == "systemctl" {
		if r.systemctl == nil {
			return "", fmt.Errorf("%w: the systemctl command was not found",
				ErrNotAvailable)
		}
		return r.systemctl.Run(ctx, cmd)
	}
	if r.run == nil {
		return "", r.unavailable()
	}
	if directory.IsProvisionCommand(cmd) && r.provisionRun != nil {
		return r.provisionRun.Run(ctx, cmd)
	}
	return r.run.Run(ctx, cmd)
}

// BuildProvision turns the wizard's answers into a previewable provision. It
// is refused on a host that already serves a domain: this tool creates, it
// does not replace.
func (r *Real) BuildProvision(p directory.Provision) (runner.Command, error) {
	if r.run == nil {
		return runner.Command{}, r.unavailable()
	}
	if !r.loaded {
		return runner.Command{}, fmt.Errorf(
			"the domain has not been read yet, so whether one exists is unknown")
	}
	if r.isDC || r.reachable {
		return runner.Command{}, directory.ErrDomainExists
	}
	return directory.BuildProvisionCommand(p)
}

// EnableServiceCommand is the previewed `systemctl enable --now <unit>`
// offered after a provision, with the unit name this distribution ships.
func (r *Real) EnableServiceCommand() (runner.Command, bool) {
	if r.systemctl == nil {
		return runner.Command{}, false
	}
	unit, ok := DetectDCUnit()
	if !ok {
		return runner.Command{}, false
	}
	return runner.Command{
		Argv:        []string{"systemctl", "enable", "--now", unit},
		Description: "Enable and start " + unit,
	}, true
}

// unavailable is the one error every path takes on a machine with no
// samba-tool, worded so it names the package rather than the symptom.
func (r *Real) unavailable() error {
	if r.notAvailable != nil {
		return r.notAvailable
	}
	return fmt.Errorf("%w: the samba-tool command was not found", ErrNotAvailable)
}

// Build turns an action into a previewable command, filling in the parts of an
// intent that come from this host rather than from the user: which controller
// to talk to, and which zone is the domain's own.
func (r *Real) Build(spec directory.ActionSpec, in directory.Intent) (runner.Command, error) {
	if r.run == nil {
		return runner.Command{}, r.unavailable()
	}
	in.Server = r.opts.Server
	if in.Zone == "" {
		in.Zone = r.zone
	}
	return directory.BuildCommand(spec, in)
}

// readFunc is one samba-tool invocation. Load is written against it rather
// than against a Runner so the demo backend can walk the identical read path:
// the same subcommands in the same order, through the same parsers, differing
// only in where the bytes come from. A demo that took a shortcut past the
// parsers would be a demo of the shortcut.
type readFunc func(ctx context.Context, args ...string) (string, error)

// Load reads the domain in one pass.
func (r *Real) Load(ctx context.Context) (directory.Model, error) {
	model, zone := loadDomain(ctx, r.read, r.opts.Server)
	r.zone = zone
	r.loaded, r.isDC, r.reachable = true, model.Domain.IsDC(), model.Reachable
	return model, nil
}

// loadDomain runs the read path and returns what it parsed, plus the domain's
// zone name.
//
// Every step is allowed to fail on its own. A machine where `drs showrepl` is
// refused still has accounts worth showing, and a domain that is unreachable
// still has a role and a version worth reporting — so a failure becomes a note
// beside the part it belongs to, and the read continues. The only thing that
// stops it is samba-tool not being there at all, which is not an error either:
// it is what most machines are, and the model says so.
func loadDomain(ctx context.Context, read readFunc, server string) (directory.Model, string) {
	model := directory.Model{}

	version, err := read(ctx, "--version")
	if err != nil {
		model.Detail = runner.FirstLine(err.Error())
		return model, ""
	}
	model.Installed = true
	model.Version = lastLine(version)

	if out, err := read(ctx, "testparm", "--suppress-prompt"); err != nil {
		model.Notes = append(model.Notes, "testparm: "+runner.FirstLine(err.Error()))
	} else {
		role, dnsBackend, realm, workgroup := ParseServerRole(out)
		model.Domain.ServerRole = role
		model.Domain.DNSBackend = dnsBackend
		if realm != "" {
			model.Domain.Realm = strings.ToLower(realm)
		}
		if workgroup != "" {
			model.Domain.NetBIOS = workgroup
		}
	}

	if out, err := read(ctx, "domain", "info", server); err != nil {
		model.Notes = append(model.Notes, "domain info: "+runner.FirstLine(err.Error()))
	} else if info := ParseDomainInfo(out); info.Realm != "" || info.DCName != "" {
		model.Reachable = true
		merge(&model.Domain, info)
	} else {
		// samba-tool answered, but not with a domain: on a host that is not a
		// controller this is where "Invalid IP address" arrives, and it is the
		// true reason rather than a parse failure.
		model.Notes = append(model.Notes, "domain info: "+runner.FirstLine(out))
	}

	if out, err := read(ctx, "domain", "level", "show"); err != nil {
		model.Notes = append(model.Notes, "domain level: "+runner.FirstLine(err.Error()))
	} else {
		model.Domain.ForestLevel, model.Domain.DomainLevel,
			model.Domain.LowestLevel = ParseDomainLevel(out)
	}

	// The password policy is only readable where there is a directory to ask,
	// which is a controller. On any other machine the command would fail with
	// a reason less true than "this host is not a domain controller", which
	// the role line already says.
	if model.Domain.IsDC() {
		if out, err := read(ctx, "domain", "passwordsettings", "show"); err != nil {
			model.Notes = append(model.Notes,
				"passwordsettings: "+runner.FirstLine(err.Error()))
		} else {
			model.Policy = ParsePasswordSettings(out)
		}
	}

	zone := strings.ToLower(firstNonEmpty(model.Domain.Realm, model.Domain.Forest))

	if out, err := read(ctx, "user", "list"); err != nil {
		model.Notes = append(model.Notes, "user list: "+runner.FirstLine(err.Error()))
	} else {
		for _, name := range ParseNameList(out) {
			model.Users = append(model.Users, directory.User{Name: name})
		}
		directory.SortUsers(model.Users)
	}

	if out, err := read(ctx, "group", "list"); err != nil {
		model.Notes = append(model.Notes, "group list: "+runner.FirstLine(err.Error()))
	} else {
		for _, name := range ParseNameList(out) {
			model.Groups = append(model.Groups, directory.Group{Name: name})
		}
		directory.SortGroups(model.Groups)
	}

	if out, err := read(ctx, "computer", "list"); err != nil {
		model.Notes = append(model.Notes, "computer list: "+runner.FirstLine(err.Error()))
	} else {
		for _, name := range ParseNameList(out) {
			model.Computers = append(model.Computers,
				directory.Computer{Name: strings.TrimSuffix(name, "$")})
		}
		directory.SortComputers(model.Computers)
	}

	model.Zone.Name = zone
	if zone == "" {
		// Without a realm there is no zone to ask about, and asking anyway
		// would produce a usage error rather than a useful note.
		model.Notes = append(model.Notes,
			"dns query: the domain's zone name is not known yet")
	} else if out, err := read(ctx, "dns", "query", server, zone, "@", "ALL"); err != nil {
		model.Notes = append(model.Notes, "dns query: "+runner.FirstLine(err.Error()))
	} else {
		model.Zone.Records = ParseDNSQuery(out)
		model.Zone.Read = true
		directory.SortRecords(model.Zone.Records)
	}

	if out, err := read(ctx, "drs", "showrepl"); err != nil {
		model.Repl.Detail = runner.FirstLine(err.Error())
		model.Notes = append(model.Notes, "drs showrepl: "+model.Repl.Detail)
	} else {
		model.Repl = ParseShowRepl(out)
	}

	return model, zone
}

// ShowUser reads one account in full.
func (r *Real) ShowUser(ctx context.Context, name string) (directory.User, error) {
	out, err := r.read(ctx, "user", "show", name)
	if err != nil {
		return directory.User{Name: name}, err
	}
	return ParseUserShow(name, out), nil
}

// ShowComputer reads one machine account in full.
func (r *Real) ShowComputer(ctx context.Context, name string) (directory.Computer, error) {
	out, err := r.read(ctx, "computer", "show", name)
	if err != nil {
		return directory.Computer{Name: name}, err
	}
	return ParseComputerShow(name, out), nil
}

// GroupMembers reads one group's membership.
func (r *Real) GroupMembers(ctx context.Context, name string) ([]string, error) {
	out, err := r.read(ctx, "group", "listmembers", name)
	if err != nil {
		return nil, err
	}
	return ParseNameList(out), nil
}

// read runs one samba-tool subcommand and returns what it printed. It is the
// single entry point for the read path, so a subcommand cannot be run from
// anywhere that has not been through it.
func (r *Real) read(ctx context.Context, args ...string) (string, error) {
	if r.run == nil {
		return "", r.unavailable()
	}
	argv := append([]string{directory.Bin}, args...)
	return r.run.Read(ctx, argv...)
}

// merge fills the fields a `domain info` answer knows over the ones this
// host's own configuration guessed. The controller's answer wins where the two
// disagree: it is the domain speaking, and the local file is only a claim
// about this machine.
func merge(into *directory.Domain, from directory.Domain) {
	if from.Forest != "" {
		into.Forest = from.Forest
	}
	if from.Realm != "" {
		into.Realm = from.Realm
	}
	if from.NetBIOS != "" {
		into.NetBIOS = from.NetBIOS
	}
	into.DCName = from.DCName
	into.DCNetBIOS = from.DCNetBIOS
	into.ServerSite = from.ServerSite
	into.ClientSite = from.ClientSite
}

// lastLine is what `samba-tool --version` leaves behind: on some versions it
// prints a usage complaint first and the version last.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}
