package samba

import (
	"context"
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
