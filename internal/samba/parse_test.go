package samba

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// read loads a fixture. Every one of them is either real samba-tool output
// captured from a throwaway domain controller or, for the three that needed a
// running one, constructed from the documented format — testdata/README.md
// says which is which, and that distinction is the point of having it written
// down.
func read(t *testing.T, name string) string {
	t.Helper()
	// #nosec G304 -- a fixture path built from this package's own testdata.
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return string(data)
}

func TestParseDomainInfo(t *testing.T) {
	got := ParseDomainInfo(read(t, "domain-info.txt"))
	if got.Realm != "lab.example" {
		t.Errorf("realm = %q, want lab.example", got.Realm)
	}
	if got.NetBIOS != "LAB" {
		t.Errorf("netbios = %q, want LAB", got.NetBIOS)
	}
	if got.DCName != "dc1.lab.example" {
		t.Errorf("dc name = %q", got.DCName)
	}
	if got.DCNetBIOS != "DC1" {
		t.Errorf("dc netbios = %q", got.DCNetBIOS)
	}
	if got.ServerSite != "Default-First-Site-Name" {
		t.Errorf("server site = %q", got.ServerSite)
	}
}

// TestParseDomainInfoUnreachable is the captured answer from a host where no
// controller replied. It must not look like a domain: a tool that reported an
// empty realm as a realm would show a blank domain screen and call it read.
func TestParseDomainInfoUnreachable(t *testing.T) {
	got := ParseDomainInfo(read(t, "domain-info-unreachable.txt"))
	if got.Realm != "" || got.DCName != "" {
		t.Errorf("an error was parsed as a domain: %+v", got)
	}
}

func TestParseDomainLevel(t *testing.T) {
	forest, domain, lowest := ParseDomainLevel(read(t, "domain-level-show.txt"))
	for name, got := range map[string]string{
		"forest": forest, "domain": domain, "lowest": lowest,
	} {
		if got != "2008 R2" {
			t.Errorf("%s level = %q, want %q", name, got, "2008 R2")
		}
	}
}

func TestParseNameListUsers(t *testing.T) {
	got := ParseNameList(read(t, "user-list.txt"))
	want := []string{"alice", "Guest", "krbtgt", "Administrator", "svc-backup", "bob"}
	if len(got) != len(want) {
		t.Fatalf("got %d names, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestParseNameListGroups checks the one thing a naive split gets wrong: a
// directory group name contains spaces, and every builtin one does.
func TestParseNameListGroups(t *testing.T) {
	got := ParseNameList(read(t, "group-list.txt"))
	if len(got) != 38 {
		t.Fatalf("got %d groups, want 38", len(got))
	}
	if !contains(got, "Denied RODC Password Replication Group") {
		t.Error("a group name with spaces was lost")
	}
	if !contains(got, "Helpdesk") || !contains(got, "Backup Operators Lab") {
		t.Error("a group somebody created was lost")
	}
}

func TestParseNameListComputers(t *testing.T) {
	got := ParseNameList(read(t, "computer-list.txt"))
	for _, want := range []string{"WS01$", "WS02$", "DC1$"} {
		if !contains(got, want) {
			t.Errorf("computer %q missing from %q", want, got)
		}
	}
}

func TestParseUserShow(t *testing.T) {
	user := ParseUserShow("alice", read(t, "user-show-alice.txt"))
	if user.Name != "alice" {
		t.Errorf("name = %q", user.Name)
	}
	if user.DisplayName != "Alice Nunes" {
		t.Errorf("display name = %q", user.DisplayName)
	}
	if user.Mail != "alice@lab.example" {
		t.Errorf("mail = %q", user.Mail)
	}
	if user.UAC != 512 || user.Disabled {
		t.Errorf("uac = %d, disabled = %v; want 512 and enabled", user.UAC, user.Disabled)
	}
	if user.State() != "enabled" {
		t.Errorf("state = %q", user.State())
	}
	// accountExpires is the maximum, which means it never does.
	if user.Expires != "" {
		t.Errorf("expires = %q, want an account that never expires", user.Expires)
	}
	if user.PasswordLast == "" {
		t.Error("pwdLastSet was not rendered")
	}
	if len(user.Groups) != 1 || user.Groups[0] != "Helpdesk" {
		t.Errorf("groups = %q, want [Helpdesk]", user.Groups)
	}
	if user.DN != "CN=Alice Nunes,CN=Users,DC=lab,DC=example" {
		t.Errorf("dn = %q", user.DN)
	}
}

// TestParseUserShowMultipleMemberOf is the Administrator, who is in five
// groups: every one of them has to survive, because "which groups is this
// account in" is the question the screen exists to answer.
func TestParseUserShowMultipleMemberOf(t *testing.T) {
	user := ParseUserShow("Administrator", read(t, "user-show-administrator.txt"))
	want := []string{"Domain Admins", "Schema Admins", "Enterprise Admins",
		"Group Policy Creator Owners", "Administrators"}
	if len(user.Groups) != len(want) {
		t.Fatalf("groups = %q, want %d of them", user.Groups, len(want))
	}
	for i := range want {
		if user.Groups[i] != want[i] {
			t.Errorf("group %d = %q, want %q", i, user.Groups[i], want[i])
		}
	}
	if user.Description == "" {
		t.Error("the built-in description was lost")
	}
}

func TestParseComputerShow(t *testing.T) {
	computer := ParseComputerShow("WS01", read(t, "computer-show-ws01.txt"))
	if computer.Name != "WS01" {
		t.Errorf("name = %q, want WS01 without the trailing $", computer.Name)
	}
	// 4098 is WORKSTATION_TRUST_ACCOUNT | ACCOUNTDISABLE: samba-tool creates a
	// machine account disabled until the machine joins.
	if !computer.Disabled {
		t.Error("userAccountControl 4098 has the disable bit set")
	}
	if computer.DN != "CN=WS01,CN=Computers,DC=lab,DC=example" {
		t.Errorf("dn = %q", computer.DN)
	}
}

func TestParseDNSQuery(t *testing.T) {
	records := ParseDNSQuery(read(t, "dns-query.txt"))
	if len(records) != 7 {
		t.Fatalf("got %d records, want 7: %+v", len(records), records)
	}
	apex := 0
	for _, record := range records {
		if record.Node == "@" {
			apex++
		}
	}
	if apex != 3 {
		t.Errorf("%d records at the apex, want 3", apex)
	}
	var ws01 int
	for _, record := range records {
		if record.Node == "ws01" {
			ws01++
			if record.Type != "A" || record.Data != "10.10.0.21" {
				t.Errorf("ws01 = %+v", record)
			}
			if record.TTL != 900 {
				t.Errorf("ws01 ttl = %d, want 900", record.TTL)
			}
		}
	}
	if ws01 != 1 {
		t.Errorf("%d ws01 records, want 1", ws01)
	}
}

// TestParseDNSQueryRefused is the captured answer from a host whose DNS RPC
// server is not listening. Nothing in it is a record.
func TestParseDNSQueryRefused(t *testing.T) {
	if records := ParseDNSQuery(read(t, "dns-query-refused.txt")); len(records) != 0 {
		t.Errorf("an error was parsed as %d records: %+v", len(records), records)
	}
}

func TestParseShowRepl(t *testing.T) {
	repl := ParseShowRepl(read(t, "drs-showrepl.txt"))
	if !repl.Read {
		t.Fatal("showrepl was not marked read")
	}
	if repl.Site != "Default-First-Site-Name" || repl.Name != "DC1" {
		t.Errorf("controller = %q\\%q", repl.Site, repl.Name)
	}
	if repl.GUID != "4b1e83a7-6c11-4f0c-9d29-2ac0f9b16d3f" {
		t.Errorf("guid = %q", repl.GUID)
	}
	if len(repl.Partitions) != 4 {
		t.Fatalf("got %d partitions, want 4: %+v", len(repl.Partitions), repl.Partitions)
	}
	if repl.OK() {
		t.Error("one partition failed, so replication is not ok")
	}

	inbound, outbound := 0, 0
	var failing []directoryPartition
	for _, partition := range repl.Partitions {
		switch partition.Direction {
		case "inbound":
			inbound++
		case "outbound":
			outbound++
		default:
			t.Errorf("partition %q has direction %q", partition.DN, partition.Direction)
		}
		if !partition.OK {
			failing = append(failing, directoryPartition{partition.DN, partition.Failures})
		}
		if partition.Neighbor != `Default-First-Site-Name\DC2` {
			t.Errorf("neighbor = %q", partition.Neighbor)
		}
		if partition.Transport != "RPC" {
			t.Errorf("transport = %q", partition.Transport)
		}
	}
	if inbound != 3 || outbound != 1 {
		t.Errorf("%d inbound and %d outbound, want 3 and 1", inbound, outbound)
	}
	if len(failing) != 1 {
		t.Fatalf("%d failing partitions, want 1: %+v", len(failing), failing)
	}
	if failing[0].dn != "CN=Configuration,DC=lab,DC=example" {
		t.Errorf("the failing partition is %q", failing[0].dn)
	}
	if failing[0].failures != 3 {
		t.Errorf("failure count = %d, want 3", failing[0].failures)
	}
}

// directoryPartition is the two fields the failure assertions above compare.
type directoryPartition struct {
	dn       string
	failures int
}

// TestParseShowReplStopsAtKCC guards the one thing that would silently double
// the partition count: the KCC section that follows the neighbours has DNs in
// it too, and none of them is a replication status.
func TestParseShowReplStopsAtKCC(t *testing.T) {
	repl := ParseShowRepl(read(t, "drs-showrepl.txt"))
	for _, partition := range repl.Partitions {
		if strings.Contains(partition.DN, "NTDS Settings") {
			t.Errorf("a KCC connection object was read as a partition: %q", partition.DN)
		}
	}
}

func TestParseShowReplFailed(t *testing.T) {
	repl := ParseShowRepl(read(t, "drs-showrepl-failed.txt"))
	if len(repl.Partitions) != 0 {
		t.Errorf("an error was parsed as %d partitions", len(repl.Partitions))
	}
	if repl.OK() {
		t.Error("a controller with no partitions read is not replicating ok")
	}
}

func TestParseServerRole(t *testing.T) {
	role, dnsBackend, realm, workgroup := ParseServerRole(read(t, "testparm.txt"))
	if role != "active directory domain controller" {
		t.Errorf("role = %q", role)
	}
	if realm != "LAB.EXAMPLE" {
		t.Errorf("realm = %q", realm)
	}
	if workgroup != "LAB" {
		t.Errorf("workgroup = %q", workgroup)
	}
	if dnsBackend != "SAMBA_INTERNAL" {
		t.Errorf("dns backend = %q", dnsBackend)
	}
}

func TestLastLineVersion(t *testing.T) {
	// samba-tool prints a usage complaint before the version on some releases,
	// which is why the version is the last line and not the first.
	got := lastLine(read(t, "version.txt"))
	if got != "4.22.10-Debian-4.22.10+dfsg-0+deb13u2" {
		t.Errorf("version = %q", got)
	}
}

func TestFirstRDN(t *testing.T) {
	for _, tc := range []struct{ dn, want string }{
		{"CN=Helpdesk,CN=Users,DC=lab,DC=example", "Helpdesk"},
		{`CN=Doe\, Jane,CN=Users,DC=lab,DC=example`, "Doe, Jane"},
		{"OU=Sales,DC=lab,DC=example", "Sales"},
		{"", ""},
		{"nonsense", "nonsense"},
	} {
		if got := FirstRDN(tc.dn); got != tc.want {
			t.Errorf("FirstRDN(%q) = %q, want %q", tc.dn, got, tc.want)
		}
	}
}

// TestParseLDIFContinuation covers the two pieces of LDIF a split on ":" gets
// wrong: a value wrapped onto a continuation line, and a base64 one.
func TestParseLDIFContinuation(t *testing.T) {
	entry := ParseLDIF("dn: CN=Long,CN=Users,DC=lab,DC=example\n" +
		"description: a description long enough that samba-tool\n" +
		"  wrapped it onto a second line\n" +
		"displayName:: TG9uZyBOYW1l\n" +
		"broken:: not base64 at all\n")
	if got := entry.First("description"); got !=
		"a description long enough that samba-tool wrapped it onto a second line" {
		t.Errorf("continuation not joined: %q", got)
	}
	if got := entry.First("displayName"); got != "Long Name" {
		t.Errorf("base64 value = %q, want %q", got, "Long Name")
	}
	if got := entry.First("broken"); got != "not base64 at all" {
		t.Errorf("undecodable base64 should be kept as printed, got %q", got)
	}
}
