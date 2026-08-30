package directory

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tui-tools/tui-kit/runner"
)

// The parsers live in internal/samba and carry their own targets. What lives
// here is the other half of the same contract: the step where a name that came
// out of one of those parsers, or out of a prompt somebody typed into, becomes
// the argv a confirm dialog is about to show and the runner is about to
// execute.
//
// So this target asserts what a caller may assume for any input at all: a
// refusal is empty, a success is runnable, every argument came from the input,
// and nothing that reaches an argv could make the preview say something other
// than what will run. See
// https://github.com/tui-tools/tui-kit/blob/main/templates/FUZZING.md.

// FuzzBuildCommand feeds arbitrary targets and prompt values through the one
// place in the tool that builds a command line.
func FuzzBuildCommand(f *testing.F) {
	// The shapes a directory really has.
	f.Add(0, "alice", "90", "lab.example")
	f.Add(1, "Domain Admins", "Administrator", "lab.example")
	f.Add(2, "ws01", "ws03 A 10.10.0.23", "lab.example")
	f.Add(3, "Doe, Jane", "", "")
	// And the ones it does not: nothing, whitespace, a flag, a quote, a
	// newline that would break a one-line preview into two, a NUL, a
	// separator, and something enormous.
	f.Add(4, "", "", "")
	f.Add(5, " ", " ", " ")
	f.Add(6, "--force", "--delete-everything", "-x")
	f.Add(7, "a'b\"c", "a`b$c", "a;b|c")
	f.Add(8, "a\nb", "a\rb", "a\tb")
	f.Add(9, "a\x00b", "\x00", "\x00")
	f.Add(10, "../../etc/passwd", "$(id)", "`id`")
	f.Add(11, strings.Repeat("n", 5000), strings.Repeat("v", 5000), "lab.example")

	f.Fuzz(func(t *testing.T, index int, target, value, zone string) {
		if len(Actions) == 0 {
			t.Fatal("the action table is empty")
		}
		spec := Actions[((index%len(Actions))+len(Actions))%len(Actions)]
		in := Intent{
			Target: target,
			Value:  value,
			Zone:   zone,
			Server: "127.0.0.1",
			// A DNS delete is built from a selected row rather than a prompt,
			// so its type and data arrive the same way its target does.
			Type: "A",
			Data: value,
		}

		cmd, err := BuildCommand(spec, in)
		if err != nil {
			if !reflect.DeepEqual(cmd, runner.Command{}) {
				t.Fatalf("refused with a non-empty command: %+v", cmd)
			}
			return
		}

		if len(cmd.Argv) == 0 || cmd.Argv[0] != Bin {
			t.Fatalf("argv does not start with %s: %q", Bin, cmd.Argv)
		}
		if cmd.Description == "" {
			t.Fatal("a command needs a description for the dialog title")
		}
		if cmd.Stdin != "" {
			t.Fatal("stdin is not shown in the preview, so nothing may set it")
		}

		for i, arg := range cmd.Argv {
			if arg == "" {
				t.Fatalf("argv[%d] is empty: %q", i, cmd.Argv)
			}
			// The preview is the promise. An argument carrying a newline, a
			// carriage return or a NUL cannot be rendered truthfully on one
			// line, so none may ever reach an argv.
			if strings.ContainsAny(arg, "\n\r\x00") {
				t.Fatalf("argv[%d] = %q would break the preview", i, arg)
			}
		}

		// Everything after the subcommand words either came from the input or
		// is a flag this file wrote. A value the user never supplied turning
		// up in a command line they are about to confirm is the one failure
		// this whole design exists to prevent.
		// BuildCommand trims what it was given, so the comparison is against
		// the trimmed forms.
		given := []string{strings.TrimSpace(target), strings.TrimSpace(value),
			strings.TrimSpace(zone), "127.0.0.1", "A"}
		for _, arg := range cmd.Argv {
			if strings.TrimSpace(arg) == "" {
				t.Fatalf("argv contains a blank argument: %q", cmd.Argv)
			}
			// A record type is uppercased on its way through, so it is
			// checked as a known word rather than as a copy of the input.
			if strings.HasPrefix(arg, "--") || isKnownWord(arg) ||
				KnownRecordType(arg) {
				continue
			}
			// A DNS record's data is the prompt's remaining words rejoined
			// with single spaces, so the comparison is against the value
			// normalised the same way.
			normalised := strings.Join(strings.Fields(value), " ")
			if containsString(given, arg) || strings.Contains(normalised, arg) {
				continue
			}
			t.Fatalf("argv contains %q, which came from nowhere: %q",
				arg, cmd.Argv)
		}

		// A destructive command must be marked as one, so the dialog paints
		// itself in the danger colour rather than looking like a read.
		if cmd.Destructive != spec.Destructive {
			t.Fatalf("%s: destructive = %v, spec says %v",
				spec.Action, cmd.Destructive, spec.Destructive)
		}
	})
}

// knownWords are the fixed words BuildCommand writes itself: the program, the
// subcommand nouns and their verbs. Anything else in an argv came from the
// input, and the target above checks exactly that.
var knownWords = map[string]bool{
	Bin: true, "user": true, "group": true, "dns": true,
	"create": true, "delete": true, "enable": true, "disable": true,
	"setpassword": true, "setexpiry": true, "add": true,
	"addmembers": true, "removemembers": true,
}

// isKnownWord reports one of those.
func isKnownWord(arg string) bool { return knownWords[arg] }

// containsString reports membership.
func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
