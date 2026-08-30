package samba

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tui-tools/tui-dc/internal/directory"
)

// The family rule is that every package turning bytes it did not write into
// values the tool acts on carries a Go native fuzz test, seeded from the
// package's testdata — see
// https://github.com/tui-tools/tui-kit/blob/main/templates/FUZZING.md.
//
// This package has five parsers, and every one of them is fed the output of a
// program on a machine that may be mid-upgrade, mid-failure or answering in a
// locale nobody expected. So there is one target per parser, and each asserts
// invariants rather than outputs: what a caller is allowed to assume for any
// input at all.
//
// The invariant they share is the one the whole tool rests on. A parser never
// panics, never hangs and never invents: every string it puts in the model
// came out of the input, so a name on screen is a name the domain printed.

// seedFromTestdata adds every fixture whose name matches a prefix, plus the
// shapes real output never has: nothing, a lone newline, a NUL, a very long
// line, and the error text samba-tool prints instead of an answer.
func seedFromTestdata(f *testing.F, prefixes ...string) {
	f.Helper()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		f.Fatalf("reading testdata: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".txt") {
			continue
		}
		matched := len(prefixes) == 0
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				matched = true
			}
		}
		if !matched {
			continue
		}
		// #nosec G304 -- a fixture path built from this package's own testdata.
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatalf("reading %s: %v", name, err)
		}
		f.Add(string(data))
	}
	f.Add("")
	f.Add("\n")
	f.Add("\x00")
	f.Add(":")
	f.Add("::")
	f.Add(strings.Repeat("a", 70000))
	f.Add("ERROR: Unable to connect\nTraceback (most recent call last):\n")
}

// containedIn reports that every value the parser produced can be found in the
// input it was given. A parser that returns something the machine never said
// is worse than one that returns nothing: the screen looks answered.
func containedIn(t *testing.T, input string, values ...string) {
	t.Helper()
	for _, value := range values {
		if value == "" {
			continue
		}
		if !strings.Contains(input, value) {
			t.Fatalf("parser invented %q, which is not in its input", value)
		}
	}
}

// FuzzParseDomainInfo covers the block of labelled lines a controller answers
// a CLDAP query with, and the error text it prints when none does.
func FuzzParseDomainInfo(f *testing.F) {
	seedFromTestdata(f, "domain-info")
	f.Fuzz(func(t *testing.T, input string) {
		got := ParseDomainInfo(input)
		containedIn(t, input, got.Forest, got.Realm, got.NetBIOS,
			got.DCName, got.DCNetBIOS, got.ServerSite, got.ClientSite)
	})
}

// FuzzParseDomainLevel covers the three functional-level lines.
func FuzzParseDomainLevel(f *testing.F) {
	seedFromTestdata(f, "domain-level")
	f.Fuzz(func(t *testing.T, input string) {
		forest, domain, lowest := ParseDomainLevel(input)
		containedIn(t, input, forest, domain, lowest)
	})
}

// FuzzParseNameList covers `user list`, `group list`, `computer list` and
// `group listmembers`, which are all the same format and all feed a table the
// user then presses a key on. A name that made it through here becomes an argv
// argument later, so the invariants are strict: no duplicates, no empty
// entries, and nothing that would break a one-line command preview.
func FuzzParseNameList(f *testing.F) {
	seedFromTestdata(f, "user-list", "group-list", "computer-list", "group-listmembers")
	f.Fuzz(func(t *testing.T, input string) {
		names := ParseNameList(input)
		seen := map[string]bool{}
		for _, name := range names {
			if name == "" {
				t.Fatal("an empty name reached the list")
			}
			if name != strings.TrimSpace(name) {
				t.Fatalf("name %q was not trimmed", name)
			}
			if strings.ContainsAny(name, "\n\r\x00\t") {
				t.Fatalf("name %q would break a command preview", name)
			}
			if seen[name] {
				t.Fatalf("name %q appears twice", name)
			}
			seen[name] = true
			containedIn(t, input, name)
		}
	})
}

// FuzzParseUserShow covers the LDIF entry `samba-tool user show` prints, which
// is the format with the most ways to be misread: continuation lines, base64
// values, repeated attributes, and a bit field that decides whether the screen
// says an account can log in.
func FuzzParseUserShow(f *testing.F) {
	seedFromTestdata(f, "user-show")
	f.Fuzz(func(t *testing.T, input string) {
		user := ParseUserShow("fallback", input)
		if user.Name == "" {
			t.Fatal("an account with no name at all reached the model")
		}
		// The bits and the booleans must agree: a screen that said "enabled"
		// for an account whose userAccountControl has the disable bit set
		// would be the worst kind of wrong this tool can be.
		if (user.UAC&0x0002 != 0) != user.Disabled {
			t.Fatalf("uac %d and disabled %v disagree", user.UAC, user.Disabled)
		}
		if state := user.State(); state != "enabled" && state != "disabled" &&
			state != "locked" {
			t.Fatalf("state = %q, which no screen knows how to draw", state)
		}
		for _, group := range user.Groups {
			if group == "" {
				t.Fatal("an empty group name reached the model")
			}
		}
	})
}

// FuzzParseComputerShow covers the same format for a machine account.
func FuzzParseComputerShow(f *testing.F) {
	seedFromTestdata(f, "computer-show")
	f.Fuzz(func(t *testing.T, input string) {
		computer := ParseComputerShow("fallback", input)
		if computer.Name == "" {
			t.Fatal("a machine account with no name at all reached the model")
		}
		if strings.HasSuffix(computer.Name, "$") {
			t.Fatalf("name %q kept the trailing $", computer.Name)
		}
	})
}

// FuzzParseDNSQuery covers the zone dump. Its records are the rows a delete is
// built from, so a record that came out of here has to be one the input
// actually contained — in all three of its fields.
func FuzzParseDNSQuery(f *testing.F) {
	seedFromTestdata(f, "dns-query")
	f.Fuzz(func(t *testing.T, input string) {
		for _, record := range ParseDNSQuery(input) {
			if record.Node == "" {
				t.Fatal("a record with no node reached the model")
			}
			if record.Type == "" {
				t.Fatal("a record with no type reached the model")
			}
			if record.TTL < 0 {
				t.Fatalf("negative ttl %d", record.TTL)
			}
			if record.Node != "@" {
				containedIn(t, input, record.Node)
			}
			containedIn(t, input, record.Data)
		}
	})
}

// FuzzParseShowRepl covers the replication answer, whose three sections have
// three different shapes and only two of which are a replication status.
func FuzzParseShowRepl(f *testing.F) {
	seedFromTestdata(f, "drs-showrepl")
	f.Fuzz(func(t *testing.T, input string) {
		repl := ParseShowRepl(input)
		if !repl.Read && len(repl.Partitions) > 0 {
			t.Fatal("partitions were returned from output that was not read")
		}
		for _, partition := range repl.Partitions {
			if partition.DN == "" {
				t.Fatal("a partition with no naming context reached the model")
			}
			if partition.Direction != "inbound" && partition.Direction != "outbound" {
				t.Fatalf("direction = %q", partition.Direction)
			}
			if partition.Failures < 0 {
				t.Fatalf("negative failure count %d", partition.Failures)
			}
			containedIn(t, input, partition.DN, partition.Neighbor,
				partition.Transport, partition.LastAttempt, partition.LastSuccess)
		}
		containedIn(t, input, repl.Site, repl.Name, repl.GUID)
	})
}

// FuzzParseServerRole covers `testparm`, which is where the tool learns
// whether this host is a domain controller at all.
func FuzzParseServerRole(f *testing.F) {
	seedFromTestdata(f, "testparm")
	f.Fuzz(func(t *testing.T, input string) {
		role, dnsBackend, realm, workgroup := ParseServerRole(input)
		containedIn(t, input, role, realm, workgroup)
		if dnsBackend != "" && dnsBackend != "SAMBA_INTERNAL" {
			t.Fatalf("dns backend = %q, which is not a value this parser sets",
				dnsBackend)
		}
		// The role decides whether the tool calls this machine a controller,
		// so the derived answer has to follow the string it came from.
		domain := directory.Domain{ServerRole: role}
		if domain.IsDC() != strings.Contains(strings.ToLower(role), "domain controller") {
			t.Fatalf("IsDC disagrees with role %q", role)
		}
	})
}
