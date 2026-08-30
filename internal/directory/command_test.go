package directory

import (
	"strings"
	"testing"
)

// specFor finds an action's spec by its action name, which is how a test names
// what it is testing without depending on which key it happens to be bound to.
func specFor(t *testing.T, action Action) ActionSpec {
	t.Helper()
	for _, spec := range Actions {
		if spec.Action == action {
			return spec
		}
	}
	t.Fatalf("no spec for action %q", action)
	return ActionSpec{}
}

// TestBuildCommandArgv is the assertion that matters most in this package: the
// argv a confirm dialog is about to show, for every action, spelled out.
// Anything that changes one of these lines changes what a user's key press
// does, and should have to say so here first.
func TestBuildCommandArgv(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action Action
		intent Intent
		want   string
	}{
		{"create an account", UserCreate, Intent{Value: "carol"},
			"samba-tool user create carol --random-password"},
		{"delete an account", UserDelete, Intent{Target: "carol"},
			"samba-tool user delete carol"},
		{"enable", UserEnable, Intent{Target: "bob"},
			"samba-tool user enable bob"},
		{"disable", UserDisable, Intent{Target: "bob"},
			"samba-tool user disable bob"},
		{"reset a password", UserSetPassword, Intent{Target: "alice"},
			"samba-tool user setpassword alice --random-password"},
		{"expire in 90 days", UserSetExpiry, Intent{Target: "alice", Value: "90"},
			"samba-tool user setexpiry alice --days=90"},
		{"never expire", UserSetExpiry, Intent{Target: "alice"},
			"samba-tool user setexpiry alice --noexpiry"},
		{"create a group", GroupCreate, Intent{Value: "Helpdesk"},
			"samba-tool group add Helpdesk"},
		{"delete a group", GroupDelete, Intent{Target: "Helpdesk"},
			"samba-tool group delete Helpdesk"},
		{"add a member", GroupAddMember, Intent{Target: "Helpdesk", Value: "alice"},
			"samba-tool group addmembers Helpdesk alice"},
		{"remove a member", GroupRemoveMember,
			Intent{Target: "Helpdesk", Value: "alice"},
			"samba-tool group removemembers Helpdesk alice"},
		{"add a record", DNSAdd,
			Intent{Value: "ws03 a 10.10.0.23", Zone: "lab.example", Server: "127.0.0.1"},
			"samba-tool dns add 127.0.0.1 lab.example ws03 A 10.10.0.23"},
		{"delete a record", DNSDelete,
			Intent{Target: "ws03", Type: "A", Data: "10.10.0.23",
				Zone: "lab.example", Server: "127.0.0.1"},
			"samba-tool dns delete 127.0.0.1 lab.example ws03 A 10.10.0.23"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := BuildCommand(specFor(t, tc.action), tc.intent)
			if err != nil {
				t.Fatalf("BuildCommand: %v", err)
			}
			if got := cmd.String(); got != tc.want {
				t.Errorf("argv = %q\n want %q", got, tc.want)
			}
			if cmd.Description == "" {
				t.Error("a command needs a description for the dialog title")
			}
		})
	}
}

// TestNoPasswordInArgv is a rule rather than a behaviour, so it is asserted
// rather than described. samba-tool warns about passwords on a command line
// itself, because a command line is visible in ps to every user on the
// machine; this tool never puts one there, and no future action may either.
func TestNoPasswordInArgv(t *testing.T) {
	for _, spec := range Actions {
		intent := Intent{
			Target: "alice", Value: "hunter2 A 10.10.0.9",
			Zone: "lab.example", Server: "127.0.0.1",
			Type: "A", Data: "10.10.0.9",
		}
		cmd, err := BuildCommand(spec, intent)
		if err != nil {
			continue
		}
		for _, arg := range cmd.Argv {
			lower := strings.ToLower(arg)
			if strings.HasPrefix(lower, "--newpassword") ||
				strings.HasPrefix(lower, "--password") {
				t.Errorf("%s puts a password on the command line: %q",
					spec.Action, cmd.String())
			}
		}
		if cmd.Stdin != "" {
			t.Errorf("%s writes to stdin, which the confirm dialog does not show",
				spec.Action)
		}
	}
}

// TestBuildCommandRefusals covers the inputs that must not produce a runnable
// command. A refusal returns the zero Command, so a caller that ignored the
// error still could not run anything.
func TestBuildCommandRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action Action
		intent Intent
	}{
		{"no selection", UserDelete, Intent{}},
		{"no name", UserCreate, Intent{Value: ""}},
		{"a name that reads as a flag", UserCreate, Intent{Value: "--help"}},
		{"a name with a newline", UserCreate, Intent{Value: "a\nb"}},
		{"a name with a NUL", GroupCreate, Intent{Value: "a\x00b"}},
		{"expiry that is not a number", UserSetExpiry,
			Intent{Target: "alice", Value: "soon"}},
		{"negative expiry", UserSetExpiry, Intent{Target: "alice", Value: "-3"}},
		{"a member with no group", GroupAddMember, Intent{Value: "alice"}},
		{"a group with no member", GroupAddMember, Intent{Target: "Helpdesk"}},
		{"a record with two fields", DNSAdd,
			Intent{Value: "ws03 A", Zone: "lab.example", Server: "127.0.0.1"}},
		{"a record type samba-tool will not add", DNSAdd,
			Intent{Value: "ws03 WKS 10.10.0.23", Zone: "lab.example",
				Server: "127.0.0.1"}},
		{"a record with no zone", DNSAdd, Intent{Value: "ws03 A 10.10.0.23"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := BuildCommand(specFor(t, tc.action), tc.intent)
			if err == nil {
				t.Fatalf("built %q, want a refusal", cmd.String())
			}
			if len(cmd.Argv) != 0 {
				t.Errorf("a refusal returned a runnable command: %+v", cmd)
			}
		})
	}
}

// TestActionTableIsUnambiguous guards the thing a growing action table breaks
// first: two actions on one screen bound to the same key, which would make one
// of them unreachable and the help screen a lie.
func TestActionTableIsUnambiguous(t *testing.T) {
	seen := map[Screen]map[string]Action{}
	for _, spec := range Actions {
		if spec.Key == "" {
			t.Errorf("%s has no key", spec.Action)
		}
		if spec.Label == "" || spec.Body == "" {
			t.Errorf("%s has nothing for the confirm dialog to say", spec.Action)
		}
		if spec.Needs != PromptNone && spec.PromptTitle == "" {
			t.Errorf("%s opens a prompt with no title", spec.Action)
		}
		if seen[spec.Screen] == nil {
			seen[spec.Screen] = map[string]Action{}
		}
		if other, taken := seen[spec.Screen][spec.Key]; taken {
			t.Errorf("%q on the %s screen is bound to both %s and %s",
				spec.Key, spec.Screen.Title(), other, spec.Action)
		}
		seen[spec.Screen][spec.Key] = spec.Action

		// A key an action takes cannot also be a navigation key, or the
		// reader loses a way to move around exactly on the screens that have
		// the most to move around in.
		for _, reserved := range []string{"q", "?", "/", "r", "j", "k", "g",
			"G", "tab", "enter", "esc"} {
			if spec.Key == reserved {
				t.Errorf("%s takes %q, which is a navigation key",
					spec.Action, reserved)
			}
		}
	}
}

// TestActionsForScreen checks that every action is reachable from the screen
// it belongs to and from no other.
func TestActionsForScreen(t *testing.T) {
	for _, spec := range Actions {
		if _, ok := ActionFor(spec.Screen, spec.Key); !ok {
			t.Errorf("%s is not reachable on its own screen", spec.Action)
		}
		if _, ok := ActionFor(ScreenDomain, spec.Key); ok &&
			spec.Screen != ScreenDomain {
			t.Errorf("%q leaked onto the read-only domain screen", spec.Key)
		}
		if _, ok := ActionFor(ScreenRepl, spec.Key); ok && spec.Screen != ScreenRepl {
			t.Errorf("%q leaked onto the read-only replication screen", spec.Key)
		}
	}
}

// TestReadOnlyScreensHaveNoActions states the phase-one boundary as a test:
// the domain, computers and replication screens read and do not change.
func TestReadOnlyScreensHaveNoActions(t *testing.T) {
	for _, screen := range []Screen{ScreenDomain, ScreenComputers, ScreenRepl} {
		if specs := ActionsFor(screen); len(specs) != 0 {
			t.Errorf("the %s screen has %d actions, and is meant to be read-only",
				screen.Title(), len(specs))
		}
	}
}

func TestParseRecordSpec(t *testing.T) {
	node, recordType, data, err := ParseRecordSpec("  ws03   a   10.10.0.23  ")
	if err != nil {
		t.Fatalf("ParseRecordSpec: %v", err)
	}
	if node != "ws03" || recordType != "A" || data != "10.10.0.23" {
		t.Errorf("got %q %q %q", node, recordType, data)
	}
	if _, _, _, err := ParseRecordSpec("ws03 A"); err == nil {
		t.Error("a record with no data was accepted")
	}
}
