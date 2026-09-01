package directory

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/tui-tools/tui-kit/runner"
)

// Screen is one of the six views the tool is made of. They are tabs because
// they answer six separate questions about the same domain, and a reader
// arrives with one of them already in mind.
type Screen int

// The screens, in tab order.
const (
	ScreenDomain Screen = iota
	ScreenUsers
	ScreenGroups
	ScreenComputers
	ScreenDNS
	ScreenRepl
	ScreenCount
)

// Title names a screen for the tab bar.
func (s Screen) Title() string {
	switch s {
	case ScreenUsers:
		return "users"
	case ScreenGroups:
		return "groups"
	case ScreenComputers:
		return "computers"
	case ScreenDNS:
		return "dns"
	case ScreenRepl:
		return "replication"
	default:
		return "domain"
	}
}

// Action is something the user can do to the directory.
type Action string

// The mutations this tool offers. Every one of them is a single samba-tool
// invocation, previewed before it runs.
const (
	UserCreate      Action = "user-create"
	UserDelete      Action = "user-delete"
	UserEnable      Action = "user-enable"
	UserDisable     Action = "user-disable"
	UserSetPassword Action = "user-setpassword"
	UserSetExpiry   Action = "user-setexpiry"

	GroupCreate       Action = "group-create"
	GroupDelete       Action = "group-delete"
	GroupAddMember    Action = "group-addmembers"
	GroupRemoveMember Action = "group-removemembers"

	DNSAdd    Action = "dns-add"
	DNSDelete Action = "dns-delete"

	// PasswordPolicySet changes one line of the domain's password policy.
	PasswordPolicySet Action = "passwordsettings-set"
)

// Prompt says what an action needs from the user beyond the selected row.
type Prompt int

// The prompts an action can open before it can be built.
const (
	// PromptNone builds straight from the selection.
	PromptNone Prompt = iota
	// PromptName asks for the name of a thing that does not exist yet.
	PromptName
	// PromptMember asks for an account name to add to or remove from a group.
	PromptMember
	// PromptDays asks for a number of days.
	PromptDays
	// PromptRecord asks for "name type data", which is what a DNS record is.
	PromptRecord
	// PromptPolicyValue asks for a password-policy value; what it takes
	// depends on which setting is selected, and the prompt says so.
	PromptPolicyValue
)

// ActionSpec describes one action for the key map, the help screen and the
// confirm dialog, so the three cannot drift apart.
type ActionSpec struct {
	Action Action
	// Screen is the tab the key is bound on. The same key means different
	// things on different screens, which is why it is part of the spec rather
	// than a lookup by key alone.
	Screen Screen
	Key    string
	// Label is the confirm dialog's title and the help bar's word.
	Label string
	// Body explains what will happen, above the command preview.
	Body string
	// Destructive paints the dialog in the danger color.
	Destructive bool
	// Needs is what to ask for before the command can be built.
	Needs Prompt
	// PromptTitle and PromptHelp are what the input dialog says.
	PromptTitle string
	PromptHelp  string
	// NeedsSelection reports that the action applies to the highlighted row.
	NeedsSelection bool
}

// Actions is the action table, in help-screen order. Adding a row here adds
// the key, the help entry and the confirm dialog at once.
var Actions = []ActionSpec{
	{Action: UserCreate, Screen: ScreenUsers, Key: "n", Label: "Create account",
		Needs: PromptName, PromptTitle: "New account",
		PromptHelp: "The account is created with a random password, which samba-tool prints once.",
		Body: "A new account is created and enabled. samba-tool generates the password " +
			"and prints it; this tool never puts a password on a command line, because a " +
			"command line is visible in ps to every user on the machine."},
	{Action: UserDelete, Screen: ScreenUsers, Key: "d", Label: "Delete account",
		NeedsSelection: true, Destructive: true,
		Body: "The account and everything attached to it are removed from the directory. " +
			"This cannot be undone from here."},
	{Action: UserEnable, Screen: ScreenUsers, Key: "e", Label: "Enable account",
		NeedsSelection: true,
		Body:           "The account can log in again."},
	{Action: UserDisable, Screen: ScreenUsers, Key: "s", Label: "Suspend account",
		NeedsSelection: true, Destructive: true,
		Body: "The account stays in the directory but can no longer log in."},
	{Action: UserSetPassword, Screen: ScreenUsers, Key: "p", Label: "Reset password",
		NeedsSelection: true, Destructive: true,
		Body: "samba-tool generates a new random password and prints it once. The old " +
			"password stops working immediately."},
	{Action: UserSetExpiry, Screen: ScreenUsers, Key: "x", Label: "Set expiry",
		NeedsSelection: true, Needs: PromptDays, PromptTitle: "Expire in (days)",
		PromptHelp: "A number of days, or empty for an account that never expires.",
		Body:       "The account stops working on the day given."},

	{Action: GroupCreate, Screen: ScreenGroups, Key: "n", Label: "Create group",
		Needs: PromptName, PromptTitle: "New group",
		PromptHelp: "The group name, as it will appear in the directory.",
		Body:       "An empty global security group is created."},
	{Action: GroupDelete, Screen: ScreenGroups, Key: "d", Label: "Delete group",
		NeedsSelection: true, Destructive: true,
		Body: "The group is removed. Its members keep their accounts and lose whatever " +
			"the group granted them."},
	{Action: GroupAddMember, Screen: ScreenGroups, Key: "a", Label: "Add member",
		NeedsSelection: true, Needs: PromptMember, PromptTitle: "Add member",
		PromptHelp: "An account or group name that already exists.",
		Body:       "The named account is added to the selected group."},
	{Action: GroupRemoveMember, Screen: ScreenGroups, Key: "m", Label: "Remove member",
		NeedsSelection: true, Needs: PromptMember, Destructive: true,
		PromptTitle: "Remove member",
		PromptHelp:  "One of the members listed for this group.",
		Body:        "The named account loses whatever the group granted it."},

	{Action: PasswordPolicySet, Screen: ScreenDomain, Key: "e", Label: "Edit policy setting",
		NeedsSelection: true, Needs: PromptPolicyValue, Destructive: true,
		PromptTitle: "New value",
		PromptHelp:  "The new value for the selected setting; `default` restores Samba's default.",
		Body: "One line of the domain's password policy changes, for every account in it. " +
			"A stricter policy does not lock anyone out today; a looser one weakens " +
			"every password from now on."},

	{Action: DNSAdd, Screen: ScreenDNS, Key: "n", Label: "Add record",
		Needs: PromptRecord, PromptTitle: "Add record",
		PromptHelp: "name type data — for example: ws03 A 10.10.0.23",
		Body:       "The record is added to the domain zone on this controller."},
	{Action: DNSDelete, Screen: ScreenDNS, Key: "d", Label: "Delete record",
		NeedsSelection: true, Destructive: true,
		Body: "The selected record is removed from the domain zone. Deleting the wrong " +
			"one can make the domain unreachable for its members."},
}

// ActionsFor returns the actions bound on a screen, in table order.
func ActionsFor(screen Screen) []ActionSpec {
	var specs []ActionSpec
	for _, spec := range Actions {
		if spec.Screen == screen {
			specs = append(specs, spec)
		}
	}
	return specs
}

// ActionFor returns the spec bound to a key on a screen.
func ActionFor(screen Screen, key string) (ActionSpec, bool) {
	for _, spec := range Actions {
		if spec.Screen == screen && spec.Key == key {
			return spec, true
		}
	}
	return ActionSpec{}, false
}

// Intent is everything BuildCommand needs that did not come from the action
// table: the row the user was on, and whatever the prompt collected.
type Intent struct {
	Action Action
	// Target is the selected row's name: an account, a group, a record node.
	Target string
	// Value is what the prompt returned, uninterpreted.
	Value string
	// Zone and Server address a DNS command. Server is what `samba-tool dns`
	// takes as its first argument, and is always this host.
	Zone   string
	Server string
	// Type and Data are a DNS record's own two fields, for a delete built
	// from a selected row rather than from a prompt.
	Type string
	Data string
}

// ErrNothingSelected reports an action asked for with no row under the cursor.
var ErrNothingSelected = fmt.Errorf("nothing selected")

// Bin is the one program this tool drives.
const Bin = "samba-tool"

// BuildCommand turns an intent into the exact argv that will run. It is the
// only place in the tool that assembles a command line, it is shared by the
// real and the fake backend — so --demo previews exactly what the real thing
// would run — and it is the function the fuzz target covers.
//
// Two rules hold for every input. A refusal returns the zero Command, so a
// caller can never run something that failed to build. And no argument is ever
// a password: samba-tool is asked for a random one instead, because a command
// line is visible in ps to every user on the machine.
func BuildCommand(spec ActionSpec, in Intent) (runner.Command, error) {
	if spec.Action == "" {
		return runner.Command{}, fmt.Errorf("no action given")
	}
	if in.Action != "" && in.Action != spec.Action {
		return runner.Command{}, fmt.Errorf("intent %q does not match action %q",
			in.Action, spec.Action)
	}
	// Everything is trimmed first, and trimmed everywhere: a field that was
	// only whitespace is empty, not a valid argument made of spaces. Missing
	// one of these is how " " ends up in an argv and the preview shows a gap
	// where a zone name should be.
	target := strings.TrimSpace(in.Target)
	value := strings.TrimSpace(in.Value)
	in.Zone = strings.TrimSpace(in.Zone)
	in.Server = strings.TrimSpace(in.Server)
	in.Type = strings.TrimSpace(in.Type)
	in.Data = strings.TrimSpace(in.Data)

	// A name that starts with a dash would be read by samba-tool as a flag,
	// and a name with a NUL or a newline in it cannot be shown truthfully in
	// a one-line preview. Both are refused here rather than quoted, because
	// the preview is the promise and an unshowable command line breaks it.
	for _, s := range []string{target, value, in.Zone, in.Server, in.Type, in.Data} {
		if err := checkArgument(s); err != nil {
			return runner.Command{}, err
		}
	}

	var argv []string
	var description string

	switch spec.Action {
	case UserCreate:
		if value == "" {
			return runner.Command{}, fmt.Errorf("an account needs a name")
		}
		argv = []string{Bin, "user", "create", value, "--random-password"}
		description = "Create account " + value
	case UserDelete, UserEnable, UserDisable:
		if target == "" {
			return runner.Command{}, ErrNothingSelected
		}
		verb := map[Action]string{
			UserDelete: "delete", UserEnable: "enable", UserDisable: "disable",
		}[spec.Action]
		argv = []string{Bin, "user", verb, target}
		description = spec.Label + " " + target
	case UserSetPassword:
		if target == "" {
			return runner.Command{}, ErrNothingSelected
		}
		argv = []string{Bin, "user", "setpassword", target, "--random-password"}
		description = "Reset the password of " + target
	case UserSetExpiry:
		if target == "" {
			return runner.Command{}, ErrNothingSelected
		}
		if value == "" {
			argv = []string{Bin, "user", "setexpiry", target, "--noexpiry"}
			description = target + " never expires"
			break
		}
		days, err := strconv.Atoi(value)
		if err != nil || days < 0 {
			return runner.Command{}, fmt.Errorf(
				"expiry is a number of days, not %q", in.Value)
		}
		argv = []string{Bin, "user", "setexpiry", target,
			"--days=" + strconv.Itoa(days)}
		description = fmt.Sprintf("Expire %s in %d days", target, days)

	case GroupCreate:
		if value == "" {
			return runner.Command{}, fmt.Errorf("a group needs a name")
		}
		argv = []string{Bin, "group", "add", value}
		description = "Create group " + value
	case GroupDelete:
		if target == "" {
			return runner.Command{}, ErrNothingSelected
		}
		argv = []string{Bin, "group", "delete", target}
		description = "Delete group " + target
	case GroupAddMember, GroupRemoveMember:
		if target == "" {
			return runner.Command{}, ErrNothingSelected
		}
		if value == "" {
			return runner.Command{}, fmt.Errorf("no member named")
		}
		verb := "addmembers"
		description = "Add " + value + " to " + target
		if spec.Action == GroupRemoveMember {
			verb = "removemembers"
			description = "Remove " + value + " from " + target
		}
		argv = []string{Bin, "group", verb, target, value}

	case PasswordPolicySet:
		if target == "" {
			return runner.Command{}, ErrNothingSelected
		}
		field, ok := PolicyFieldByName(target)
		if !ok {
			// The target is a flag name from the policy table, never free
			// text. Anything else is refused so no prompt, parser or future
			// caller can steer this into an argument samba-tool would read
			// as some other flag.
			return runner.Command{}, fmt.Errorf(
				"%q is not a password-policy setting this tool edits", target)
		}
		if err := ValidatePolicyValue(field, value); err != nil {
			return runner.Command{}, err
		}
		argv = []string{Bin, "domain", "passwordsettings", "set",
			"--" + field.Name + "=" + value}
		description = "Set " + strings.ToLower(field.Label) + " to " + value

	case DNSAdd:
		node, recordType, data, err := ParseRecordSpec(in.Value)
		if err != nil {
			return runner.Command{}, err
		}
		if in.Zone == "" || in.Server == "" {
			return runner.Command{}, fmt.Errorf("no zone to add to")
		}
		argv = []string{Bin, "dns", "add", in.Server, in.Zone, node, recordType, data}
		description = fmt.Sprintf("Add %s %s %s to %s", node, recordType, data, in.Zone)
	case DNSDelete:
		if target == "" || in.Type == "" || in.Data == "" {
			return runner.Command{}, ErrNothingSelected
		}
		if in.Zone == "" || in.Server == "" {
			return runner.Command{}, fmt.Errorf("no zone to delete from")
		}
		argv = []string{Bin, "dns", "delete", in.Server, in.Zone,
			target, in.Type, in.Data}
		description = fmt.Sprintf("Delete %s %s %s from %s",
			target, in.Type, in.Data, in.Zone)

	default:
		return runner.Command{}, fmt.Errorf("unknown action %q", spec.Action)
	}

	return runner.Command{
		Argv:        argv,
		Description: description,
		Destructive: spec.Destructive,
	}, nil
}

// checkArgument refuses the two shapes a previewed argument may never have: one
// that samba-tool would read as a flag, and one that cannot be rendered on a
// single line. Both would make the confirm dialog say something other than what
// runs, which is the one thing this family does not do.
func checkArgument(s string) error {
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "-") {
		return fmt.Errorf("%q starts with a dash, which samba-tool reads as a flag", s)
	}
	if strings.ContainsAny(s, "\x00\n\r\t") {
		return fmt.Errorf("a name cannot contain a newline, a tab or a NUL")
	}
	return nil
}

// ParseRecordSpec splits the "name type data" a user typed into a DNS record's
// three fields. It is separate from BuildCommand so the prompt can reject a
// malformed line before a dialog is ever opened.
func ParseRecordSpec(s string) (node, recordType, data string, err error) {
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return "", "", "", fmt.Errorf(
			"a record is %q — for example %q", "name type data", "ws03 A 10.10.0.23")
	}
	node, recordType = fields[0], strings.ToUpper(fields[1])
	data = strings.Join(fields[2:], " ")
	if !KnownRecordType(recordType) {
		return "", "", "", fmt.Errorf("%q is not a record type samba-tool adds", fields[1])
	}
	return node, recordType, data, nil
}

// recordTypes are the types `samba-tool dns add` accepts. Anything else is
// refused at the prompt rather than sent to samba-tool to be refused there,
// so the message names the mistake instead of quoting a usage string.
var recordTypes = []string{"A", "AAAA", "CNAME", "MX", "NS", "PTR", "SRV", "TXT"}

// KnownRecordType reports whether samba-tool can add this type.
func KnownRecordType(t string) bool {
	for _, known := range recordTypes {
		if known == t {
			return true
		}
	}
	return false
}

// RecordTypes returns the types a picker offers.
func RecordTypes() []string { return append([]string(nil), recordTypes...) }

// Backend is the boundary between the UI and the machine. Load reads the
// domain; Build turns an intent into a previewable command; Run executes a
// command the user confirmed. Nothing else may change anything.
type Backend interface {
	Details

	// Name identifies the backend ("samba-tool", "demo").
	Name() string
	// Describe is the one-line summary shown in the header.
	Describe() string

	// Load reads the whole domain in one pass.
	Load(ctx context.Context) (Model, error)

	// Build turns an action and an intent into a previewable command.
	Build(spec ActionSpec, in Intent) (runner.Command, error)
	// BuildProvision turns what the wizard collected into a previewable
	// `samba-tool domain provision`. It refuses (ErrDomainExists) on a host
	// that already serves a domain.
	BuildProvision(p Provision) (runner.Command, error)
	// EnableServiceCommand is the previewable `systemctl enable --now <unit>`
	// offered after a successful provision, with the unit name this
	// distribution gives the AD DC daemon. False when systemd or the unit
	// cannot be found.
	EnableServiceCommand() (runner.Command, bool)
	// Preview renders the exact command line Run will execute.
	Preview(cmd runner.Command) string
	// Run executes a previously previewed command.
	Run(ctx context.Context, cmd runner.Command) (string, error)
}

// Details is the part of the model a screen asks for only when a row is
// opened. It is separate from Load because the cheap commands list names and
// the expensive ones describe a single object: `samba-tool user list` is one
// process, and `samba-tool user show` for every account it returned is one
// process per account. A domain with five hundred accounts would take minutes
// to open if the tool insisted on knowing everything before it drew anything.
//
// So the tables draw from the lists, and the detail arrives when the reader
// asks for it. What a row does not know yet, it says it does not know.
type Details interface {
	// ShowUser reads one account.
	ShowUser(ctx context.Context, name string) (User, error)
	// ShowComputer reads one machine account.
	ShowComputer(ctx context.Context, name string) (Computer, error)
	// GroupMembers reads one group's membership.
	GroupMembers(ctx context.Context, name string) ([]string, error)
}
