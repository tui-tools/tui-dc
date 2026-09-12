package samba

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tui-tools/tui-dc/internal/directory"
)

// TestLoadDomainRPCArgv pins the command lines of the read path, because the
// fixtures cannot. A zone dump parses the same whether or not the command that
// produced it could authenticate, so a missing `-P` — or a `drs showrepl` with
// no server to connect to — is invisible to every parser test here while being
// the difference between a DNS screen with the zone on it and an empty one on
// every real controller.
func TestLoadDomainRPCArgv(t *testing.T) {
	fake := NewFake()
	var ran [][]string
	record := func(ctx context.Context, args ...string) (string, error) {
		ran = append(ran, append([]string(nil), args...))
		return fake.read(ctx, args...)
	}

	model, zone := loadDomain(context.Background(), record, DefaultServer)
	if zone != "lab.example" {
		t.Fatalf("zone = %q, want lab.example", zone)
	}

	for _, want := range [][]string{
		{"dns", "query", DefaultServer, "lab.example", "@", "ALL",
			directory.MachineAccountFlag},
		// The server argument is as load-bearing as the flag: without it
		// samba-tool resolves this DC's own name through the host resolver.
		{"drs", "showrepl", DefaultServer, directory.MachineAccountFlag},
	} {
		if !wasRun(ran, want) {
			t.Errorf("the read path never ran %q\nit ran: %q",
				strings.Join(want, " "), ran)
		}
	}

	// The flag belongs to the two RPC commands and to nothing else: the rest of
	// the read path opens the local database, where credentials are neither
	// needed nor meaningful.
	for _, args := range ran {
		if args[0] == "dns" || args[0] == "drs" {
			continue
		}
		for _, arg := range args {
			if arg == directory.MachineAccountFlag {
				t.Errorf("%q carries %s, which only the RPC commands need",
					strings.Join(args, " "), directory.MachineAccountFlag)
			}
		}
	}

	// And the point of all of it: both screens have something to show.
	if !model.Zone.Read || len(model.Zone.Records) == 0 {
		t.Errorf("the zone was not read: %+v", model.Zone)
	}
	if !model.Repl.Read {
		t.Errorf("replication was not read: %+v", model.Repl)
	}
}

// wasRun reports whether the read path ran exactly this argument list.
func wasRun(ran [][]string, want []string) bool {
	for _, args := range ran {
		if len(args) != len(want) {
			continue
		}
		same := true
		for i := range args {
			if args[i] != want[i] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// distroSMBConfTestparm is what `samba-tool testparm --suppress-prompt` prints
// on a host running a distribution's own /etc/samba/smb.conf: the parameters
// the file sets, and not the role, which the file leaves derived. Read on the
// lab's Fedora 44 guest against the packaged file (tui-dc#17).
const distroSMBConfTestparm = "\tsecurity = USER\n\tworkgroup = SAMBA\n"

// roleReadArgv is the read that answers the role when smb.conf only implies it.
var roleReadArgv = []string{"testparm", "--suppress-prompt",
	"--parameter-name=server role"}

// TestLoadDomainRoleFromParameterRead is the unprovisioned host: samba-tool is
// installed, smb.conf is the distribution's, and the role is nowhere in the
// output the read path parses. It has to come from the one-parameter read, as
// `auto` — the word provision itself refuses on, and the word the preflight
// has to be able to print.
func TestLoadDomainRoleFromParameterRead(t *testing.T) {
	var ran [][]string
	readHost := func(_ context.Context, args ...string) (string, error) {
		ran = append(ran, append([]string(nil), args...))
		switch {
		case args[0] == "--version":
			return "4.24.6\n", nil
		case wasRun([][]string{args}, roleReadArgv):
			return read(t, "testparm-parameter-role-auto.txt"), nil
		case args[0] == "testparm":
			return distroSMBConfTestparm, nil
		}
		return "", errors.New("ERROR: Unable to open the sam database")
	}

	model, _ := loadDomain(context.Background(), readHost, DefaultServer)
	if !wasRun(ran, roleReadArgv) {
		t.Fatalf("the role was never asked for by name\nit ran: %q", ran)
	}
	if model.Domain.ServerRole != "auto" {
		t.Errorf("role = %q, want auto", model.Domain.ServerRole)
	}
	// The whole point of the value is the sentence the preflight builds from
	// it, and the whole danger of it is IsDC: `auto` is not a controller, and a
	// host where it said otherwise would be offered no provision at all.
	if model.Domain.IsDC() {
		t.Error("a host whose role is auto was taken for a domain controller")
	}
	if !model.Installed {
		t.Error("samba-tool answered --version and the model says it is absent")
	}
}

// TestLoadDomainRoleNotReadTwice is the provisioned controller, where the role
// is in its own smb.conf and the first read already has it. The second read
// must not happen: one more samba-tool process per load, for a fact already
// held, is the cost this fallback exists to avoid paying everywhere.
func TestLoadDomainRoleNotReadTwice(t *testing.T) {
	fake := NewFake()
	var ran [][]string
	record := func(ctx context.Context, args ...string) (string, error) {
		ran = append(ran, append([]string(nil), args...))
		return fake.read(ctx, args...)
	}

	model, _ := loadDomain(context.Background(), record, DefaultServer)
	if model.Domain.ServerRole != "active directory domain controller" {
		t.Errorf("role = %q", model.Domain.ServerRole)
	}
	if wasRun(ran, roleReadArgv) {
		t.Error("the role was read a second time on a host that already said it")
	}
}

// TestLoadDomainRoleReadFails is a host where the one-parameter read cannot
// run — an older samba-tool that does not take the option, a build that dies
// on an import. The load keeps going and the role stays empty, which is what
// the tool did before the read existed; the preflight's fallback wording
// covers it.
func TestLoadDomainRoleReadFails(t *testing.T) {
	readHost := func(_ context.Context, args ...string) (string, error) {
		switch {
		case args[0] == "--version":
			return "4.24.6\n", nil
		case wasRun([][]string{args}, roleReadArgv):
			return "", errors.New("Usage: samba-tool testparm [options]")
		case args[0] == "testparm":
			return distroSMBConfTestparm, nil
		}
		return "", errors.New("ERROR: Unable to open the sam database")
	}

	model, _ := loadDomain(context.Background(), readHost, DefaultServer)
	if model.Domain.ServerRole != "" {
		t.Errorf("role = %q, want it to stay empty", model.Domain.ServerRole)
	}
	if model.Domain.IsDC() {
		t.Error("a failed read made a host into a domain controller")
	}
	if !model.Installed || model.Domain.NetBIOS != "SAMBA" {
		t.Errorf("the rest of the load did not survive: %+v", model.Domain)
	}
	// The failure is silent on purpose: it refines a fact the screen already
	// shows without it, so there is nothing to tell the user about it.
	for _, note := range model.Notes {
		if strings.Contains(note, "parameter-name") {
			t.Errorf("the failed read left a note: %q", note)
		}
	}
}

// TestFakeAnswersParameterRead keeps the fake backend honest about the read the
// real one makes: --demo has to answer it the way samba does, preamble and all.
func TestFakeAnswersParameterRead(t *testing.T) {
	fresh := NewFakeFresh()
	out, err := fresh.read(context.Background(), roleReadArgv...)
	if err != nil {
		t.Fatalf("the fake refused the read: %v", err)
	}
	if got := ParseParameterValue(out); got != "standalone server" {
		t.Errorf("role = %q, want standalone server", got)
	}
	if !strings.Contains(out, "Loaded services file OK.") {
		t.Error("the fake skipped samba's logger preamble, which the parser " +
			"exists to skip")
	}
}
