package samba

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-kit/runner"
)

// Fake is the in-memory backend behind --demo and the tests. It is not a fake
// model: it is a fake samba-tool. It holds a small plausible domain and, asked
// for a subcommand, renders the text that samba-tool would have printed — which
// loadDomain then parses with the same functions it uses on a real controller.
//
// That is the point of building it this way. --demo exercises every parser in
// this package on every start, so the demo cannot drift away from the tool, and
// a parser that would break on the real thing breaks in the demo first.
//
// Nothing here reaches the machine. A confirmed command is applied to the
// in-memory domain by the runner.Fake hook, so the argv that was previewed is
// the argv that changed the state, exactly as on a real controller.
type Fake struct {
	mu  sync.Mutex
	run *runner.Fake

	realm   string
	netbios string
	dc      string
	site    string

	users     map[string]*fakeUser
	groups    map[string]*fakeGroup
	computers map[string]bool
	records   []directory.Record
}

// fakeUser is one account in the sample domain.
type fakeUser struct {
	name        string
	displayName string
	given       string
	surname     string
	mail        string
	description string
	uac         int
	expires     string
	pwdLastSet  string
	lastLogon   string
}

// fakeGroup is one group in the sample domain.
type fakeGroup struct {
	name        string
	description string
	members     []string
}

// NewFake returns a Fake preloaded with a plausible domain: a realm, a handful
// of accounts in the states worth seeing, two groups somebody made, two
// workstations, a zone, and one replication partner with one partition that is
// failing — because a replication screen that has only ever shown healthy
// output is a replication screen nobody has looked at.
func NewFake() *Fake {
	f := &Fake{
		realm:   "lab.example",
		netbios: "LAB",
		dc:      "dc1",
		site:    "Default-First-Site-Name",
		users: map[string]*fakeUser{
			"Administrator": {name: "Administrator", uac: 512,
				description: "Built-in account for administering the computer/domain",
				pwdLastSet:  "134325987549108539"},
			"Guest": {name: "Guest", uac: 66082,
				description: "Built-in account for guest access to the computer/domain"},
			"krbtgt": {name: "krbtgt", uac: 514,
				description: "Key Distribution Center Service Account"},
			"alice": {name: "alice", given: "Alice", surname: "Nunes",
				displayName: "Alice Nunes", mail: "alice@lab.example", uac: 512,
				pwdLastSet: "134325987833761618"},
			"bob": {name: "bob", given: "Bob", surname: "Silva",
				displayName: "Bob Silva", mail: "bob@lab.example", uac: 514,
				pwdLastSet: "134301004112000000"},
			"svc-backup": {name: "svc-backup", displayName: "Backup service",
				description: "Backup service account", uac: 66048,
				pwdLastSet: "134210004112000000"},
		},
		groups: map[string]*fakeGroup{
			"Domain Admins": {name: "Domain Admins", members: []string{"Administrator"},
				description: "Designated administrators of the domain"},
			"Domain Users":  {name: "Domain Users", description: "All domain users"},
			"Domain Guests": {name: "Domain Guests", description: "All domain guests"},
			"Enterprise Admins": {name: "Enterprise Admins",
				members: []string{"Administrator"}},
			"Schema Admins":    {name: "Schema Admins", members: []string{"Administrator"}},
			"Backup Operators": {name: "Backup Operators"},
			"Helpdesk": {name: "Helpdesk", description: "Second-line support",
				members: []string{"alice"}},
			"Backup Operators Lab": {name: "Backup Operators Lab",
				description: "Runs the nightly backup", members: []string{"svc-backup"}},
		},
		computers: map[string]bool{"DC1": true, "WS01": true, "WS02": true},
		records: []directory.Record{
			{Node: "@", Type: "SOA", TTL: 3600,
				Data: "serial=110, refresh=900, retry=600, expire=86400, " +
					"minttl=3600, ns=dc1.lab.example., email=hostmaster.lab.example."},
			{Node: "@", Type: "NS", Data: "dc1.lab.example.", TTL: 900},
			{Node: "@", Type: "A", Data: "10.10.0.10", TTL: 900},
			{Node: "dc1", Type: "A", Data: "10.10.0.10", TTL: 900},
			{Node: "files", Type: "CNAME", Data: "dc1.lab.example.", TTL: 3600},
			{Node: "ws01", Type: "A", Data: "10.10.0.21", TTL: 900},
			{Node: "ws02", Type: "A", Data: "10.10.0.22", TTL: 900},
		},
	}
	f.run = &runner.Fake{Hook: f.apply}
	return f
}

// Name identifies the backend.
func (f *Fake) Name() string { return "demo" }

// Describe is the one-line summary shown in the header.
func (f *Fake) Describe() string {
	return "demo — no change reaches a domain"
}

// Preview renders the command the way the real backend would.
func (f *Fake) Preview(cmd runner.Command) string { return f.run.Preview(cmd) }

// Run applies a confirmed command to the in-memory domain.
func (f *Fake) Run(ctx context.Context, cmd runner.Command) (string, error) {
	return f.run.Run(ctx, cmd)
}

// Commands returns every command the fake was asked to run, for the tests.
func (f *Fake) Commands() []runner.Command { return f.run.Ran }

// Build turns an action into a previewable command, exactly as Real does and
// through the same function.
func (f *Fake) Build(spec directory.ActionSpec, in directory.Intent) (runner.Command, error) {
	in.Server = DefaultServer
	if in.Zone == "" {
		in.Zone = f.realm
	}
	return directory.BuildCommand(spec, in)
}

// Load walks the same read path the real backend does, over this fake's
// rendered samba-tool output.
func (f *Fake) Load(ctx context.Context) (directory.Model, error) {
	model, _ := loadDomain(ctx, f.read, DefaultServer)
	return model, nil
}

// ShowUser renders and parses one account, as the real backend does.
func (f *Fake) ShowUser(ctx context.Context, name string) (directory.User, error) {
	out, err := f.read(ctx, "user", "show", name)
	if err != nil {
		return directory.User{Name: name}, err
	}
	return ParseUserShow(name, out), nil
}

// ShowComputer renders and parses one machine account.
func (f *Fake) ShowComputer(ctx context.Context, name string) (directory.Computer, error) {
	out, err := f.read(ctx, "computer", "show", name)
	if err != nil {
		return directory.Computer{Name: name}, err
	}
	return ParseComputerShow(name, out), nil
}

// GroupMembers renders and parses one group's membership.
func (f *Fake) GroupMembers(ctx context.Context, name string) ([]string, error) {
	out, err := f.read(ctx, "group", "listmembers", name)
	if err != nil {
		return nil, err
	}
	return ParseNameList(out), nil
}

// read is the fake samba-tool: a subcommand in, the text that subcommand would
// have printed out.
func (f *Fake) read(_ context.Context, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.respond(args)
}

// respond renders one subcommand's output. It is deliberately written the way
// samba-tool prints — trailing spaces in the label column of `domain info`,
// the `(flags=…, serial=…, ttl=…)` tail on every DNS record, the tab-indented
// blocks of `drs showrepl` — because anything tidier would be testing a format
// Samba does not have.
func (f *Fake) respond(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("samba-tool: missing subcommand")
	}
	switch {
	case args[0] == "--version":
		return "4.22.10-Debian-4.22.10+dfsg-0+deb13u2\n", nil

	case args[0] == "testparm":
		return "# Global parameters\n[global]\n" +
			"\tdns forwarder = 10.10.0.1\n" +
			"\tnetbios name = " + strings.ToUpper(f.dc) + "\n" +
			"\trealm = " + strings.ToUpper(f.realm) + "\n" +
			"\tserver role = active directory domain controller\n" +
			"\tworkgroup = " + f.netbios + "\n" +
			"\tidmap_ldb:use rfc2307 = yes\n", nil

	case args[0] == "domain" && len(args) > 1 && args[1] == "info":
		return "Forest           : " + f.realm + "\n" +
			"Domain           : " + f.realm + "\n" +
			"Netbios domain   : " + f.netbios + "\n" +
			"DC name          : " + f.dc + "." + f.realm + "\n" +
			"DC netbios name  : " + strings.ToUpper(f.dc) + "\n" +
			"Server site      : " + f.site + "\n" +
			"Client site      : " + f.site + "\n", nil

	case args[0] == "domain" && len(args) > 2 && args[1] == "level" && args[2] == "show":
		return "Domain and forest function level for domain 'DC=lab,DC=example'\n\n" +
			"Forest function level: (Windows) 2016\n" +
			"Domain function level: (Windows) 2016\n" +
			"Lowest function level of a DC: (Windows) 2016\n", nil

	case args[0] == "user" && len(args) > 1 && args[1] == "list":
		return joinLines(sortedKeys(f.users)), nil
	case args[0] == "user" && len(args) > 2 && args[1] == "show":
		user, ok := f.users[args[2]]
		if !ok {
			return "", fmt.Errorf("ERROR: Unable to find user '%s'", args[2])
		}
		return f.userLDIF(user), nil

	case args[0] == "group" && len(args) > 1 && args[1] == "list":
		return joinLines(sortedGroupKeys(f.groups)), nil
	case args[0] == "group" && len(args) > 2 && args[1] == "listmembers":
		group, ok := f.groups[args[2]]
		if !ok {
			return "", fmt.Errorf("ERROR: Unable to find group '%s'", args[2])
		}
		return joinLines(group.members), nil

	case args[0] == "computer" && len(args) > 1 && args[1] == "list":
		names := make([]string, 0, len(f.computers))
		for name := range f.computers {
			names = append(names, name+"$")
		}
		sort.Strings(names)
		return joinLines(names), nil
	case args[0] == "computer" && len(args) > 2 && args[1] == "show":
		name := strings.TrimSuffix(args[2], "$")
		if !f.computers[name] {
			return "", fmt.Errorf("ERROR: Unable to find computer '%s'", args[2])
		}
		return f.computerLDIF(name), nil

	case args[0] == "dns" && len(args) > 1 && args[1] == "query":
		return f.zoneDump(), nil

	case args[0] == "drs" && len(args) > 1 && args[1] == "showrepl":
		return f.showrepl(), nil
	}
	return "", fmt.Errorf("samba-tool: unknown subcommand %q", strings.Join(args, " "))
}

// apply mutates the sample domain the way the real command would. It is the
// runner.Fake hook, so it runs only for a command that was previewed and
// confirmed — the same path the real backend takes.
func (f *Fake) apply(cmd runner.Command) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	args := cmd.Argv
	if len(args) < 3 || args[0] != directory.Bin {
		return "", fmt.Errorf("samba-tool: cannot parse %q", cmd.String())
	}
	switch {
	case args[1] == "user" && args[2] == "create":
		name := args[3]
		if _, exists := f.users[name]; exists {
			return "", fmt.Errorf("ERROR: The account '%s' already exists", name)
		}
		f.users[name] = &fakeUser{name: name, uac: 512}
		return "User '" + name + "' added successfully\n" +
			"User '" + name + "' password is 'Xoh1eeph{aiL'\n", nil
	case args[1] == "user" && args[2] == "delete":
		if _, ok := f.users[args[3]]; !ok {
			return "", fmt.Errorf("ERROR: Unable to find user '%s'", args[3])
		}
		delete(f.users, args[3])
		for _, group := range f.groups {
			group.members = without(group.members, args[3])
		}
		return "Deleted user " + args[3] + "\n", nil
	case args[1] == "user" && (args[2] == "enable" || args[2] == "disable"):
		user, ok := f.users[args[3]]
		if !ok {
			return "", fmt.Errorf("ERROR: Unable to find user '%s'", args[3])
		}
		if args[2] == "enable" {
			user.uac &^= 0x0002
			return "Enabled user '" + args[3] + "'\n", nil
		}
		user.uac |= 0x0002
		return "Disabled user '" + args[3] + "'\n", nil
	case args[1] == "user" && args[2] == "setpassword":
		if _, ok := f.users[args[3]]; !ok {
			return "", fmt.Errorf("ERROR: Unable to find user '%s'", args[3])
		}
		return "Changed password OK\n" +
			"New password is 'ohGhee7ai{Qu'\n", nil
	case args[1] == "user" && args[2] == "setexpiry":
		if _, ok := f.users[args[3]]; !ok {
			return "", fmt.Errorf("ERROR: Unable to find user '%s'", args[3])
		}
		return "Expiry for user '" + args[3] + "' set\n", nil

	case args[1] == "group" && args[2] == "add":
		if _, exists := f.groups[args[3]]; exists {
			return "", fmt.Errorf("ERROR: The group '%s' already exists", args[3])
		}
		f.groups[args[3]] = &fakeGroup{name: args[3]}
		return "Added group " + args[3] + "\n", nil
	case args[1] == "group" && args[2] == "delete":
		if _, ok := f.groups[args[3]]; !ok {
			return "", fmt.Errorf("ERROR: Unable to find group '%s'", args[3])
		}
		delete(f.groups, args[3])
		return "Deleted group " + args[3] + "\n", nil
	case args[1] == "group" && args[2] == "addmembers":
		group, ok := f.groups[args[3]]
		if !ok {
			return "", fmt.Errorf("ERROR: Unable to find group '%s'", args[3])
		}
		if _, ok := f.users[args[4]]; !ok {
			return "", fmt.Errorf("ERROR: Could not add members to group %s: "+
				"Unable to find member '%s'", args[3], args[4])
		}
		group.members = append(without(group.members, args[4]), args[4])
		sort.Strings(group.members)
		return "Added members to group " + args[3] + "\n", nil
	case args[1] == "group" && args[2] == "removemembers":
		group, ok := f.groups[args[3]]
		if !ok {
			return "", fmt.Errorf("ERROR: Unable to find group '%s'", args[3])
		}
		group.members = without(group.members, args[4])
		return "Removed members from group " + args[3] + "\n", nil

	case args[1] == "dns" && args[2] == "add":
		if len(args) < 8 {
			return "", fmt.Errorf("samba-tool: dns add needs a name, a type and data")
		}
		f.records = append(f.records, directory.Record{
			Node: args[5], Type: args[6], Data: args[7], TTL: 900})
		return "Record added successfully\n", nil
	case args[1] == "dns" && args[2] == "delete":
		if len(args) < 8 {
			return "", fmt.Errorf("samba-tool: dns delete needs a name, a type and data")
		}
		kept := f.records[:0]
		found := false
		for _, record := range f.records {
			if record.Node == args[5] && record.Type == args[6] && record.Data == args[7] {
				found = true
				continue
			}
			kept = append(kept, record)
		}
		f.records = kept
		if !found {
			return "", fmt.Errorf("ERROR: Record does not exist")
		}
		return "Record deleted successfully\n", nil
	}
	return "", fmt.Errorf("samba-tool: unknown command %q", cmd.String())
}

// userLDIF renders one account the way `samba-tool user show` prints it.
func (f *Fake) userLDIF(user *fakeUser) string {
	dn := "CN=" + firstNonEmpty(user.displayName, user.name) + ",CN=Users,DC=lab,DC=example"
	var b strings.Builder
	fmt.Fprintf(&b, "dn: %s\n", dn)
	b.WriteString("objectClass: top\nobjectClass: person\n" +
		"objectClass: organizationalPerson\nobjectClass: user\n")
	fmt.Fprintf(&b, "cn: %s\n", firstNonEmpty(user.displayName, user.name))
	if user.surname != "" {
		fmt.Fprintf(&b, "sn: %s\n", user.surname)
	}
	if user.given != "" {
		fmt.Fprintf(&b, "givenName: %s\n", user.given)
	}
	if user.displayName != "" {
		fmt.Fprintf(&b, "displayName: %s\n", user.displayName)
	}
	if user.description != "" {
		fmt.Fprintf(&b, "description: %s\n", user.description)
	}
	fmt.Fprintf(&b, "instanceType: 4\nsAMAccountName: %s\n", user.name)
	fmt.Fprintf(&b, "sAMAccountType: 805306368\n")
	if user.mail != "" {
		fmt.Fprintf(&b, "mail: %s\n", user.mail)
	}
	fmt.Fprintf(&b, "userAccountControl: %d\n", user.uac)
	fmt.Fprintf(&b, "accountExpires: %s\n", firstNonEmpty(user.expires, "9223372036854775807"))
	fmt.Fprintf(&b, "pwdLastSet: %s\n", firstNonEmpty(user.pwdLastSet, "0"))
	fmt.Fprintf(&b, "lastLogon: %s\n", firstNonEmpty(user.lastLogon, "0"))
	for _, group := range sortedGroupKeys(f.groups) {
		if contains(f.groups[group].members, user.name) {
			fmt.Fprintf(&b, "memberOf: CN=%s,CN=Users,DC=lab,DC=example\n", group)
		}
	}
	fmt.Fprintf(&b, "distinguishedName: %s\n", dn)
	return b.String()
}

// computerLDIF renders one machine account the way `computer show` prints it.
func (f *Fake) computerLDIF(name string) string {
	dn := "CN=" + name + ",CN=Computers,DC=lab,DC=example"
	os, osVersion, uac := "Windows 11 Pro", "10.0 (22631)", 4096
	if strings.EqualFold(name, f.dc) {
		os, osVersion, uac = "Samba", "4.22.10", 532480
		dn = "CN=" + name + ",OU=Domain Controllers,DC=lab,DC=example"
	}
	return "dn: " + dn + "\n" +
		"objectClass: top\nobjectClass: person\n" +
		"objectClass: organizationalPerson\nobjectClass: user\nobjectClass: computer\n" +
		"cn: " + name + "\n" +
		"instanceType: 4\n" +
		"dNSHostName: " + strings.ToLower(name) + "." + f.realm + "\n" +
		"operatingSystem: " + os + "\n" +
		"operatingSystemVersion: " + osVersion + "\n" +
		"userAccountControl: " + strconv.Itoa(uac) + "\n" +
		"sAMAccountName: " + name + "$\n" +
		"sAMAccountType: 805306369\n" +
		"distinguishedName: " + dn + "\n"
}

// zoneDump renders the zone the way `samba-tool dns query … @ ALL` prints it,
// grouped by node with the flags tail on every record.
func (f *Fake) zoneDump() string {
	records := append([]directory.Record(nil), f.records...)
	directory.SortRecords(records)

	var b strings.Builder
	node := ""
	first := true
	for _, record := range records {
		if record.Node != node || first {
			node, first = record.Node, false
			name := node
			if name == "@" {
				name = ""
			}
			count := 0
			for _, other := range records {
				if other.Node == node {
					count++
				}
			}
			fmt.Fprintf(&b, "  Name=%s, Records=%d, Children=0\n", name, count)
		}
		fmt.Fprintf(&b, "    %s: %s (flags=f0, serial=110, ttl=%d)\n",
			record.Type, record.Data, record.TTL)
	}
	return b.String()
}

// showrepl renders `samba-tool drs showrepl` for a domain with one partner and
// one partition whose last attempt failed.
func (f *Fake) showrepl() string {
	const guid = "7fa4c210-3d95-42b8-9d6a-58c0f2b31a44"
	partner := f.site + `\DC2`
	head := f.site + `\` + strings.ToUpper(f.dc) + "\n" +
		"DSA Options: 0x00000001\n" +
		"DSA object GUID: 4b1e83a7-6c11-4f0c-9d29-2ac0f9b16d3f\n" +
		"DSA invocationId: 9d2ba3c4-8f60-4a2e-b7d1-1e8c7a54f0b2\n\n"

	ok := func(partition string) string {
		return partition + "\n\t" + partner + " via RPC\n" +
			"\t\tDSA object GUID: " + guid + "\n" +
			"\t\tLast attempt @ Sat Aug 30 09:12:44 2026 UTC was successful\n" +
			"\t\t0 consecutive failure(s).\n" +
			"\t\tLast success @ Sat Aug 30 09:12:44 2026 UTC\n\n"
	}
	failing := "CN=Configuration,DC=lab,DC=example\n\t" + partner + " via RPC\n" +
		"\t\tDSA object GUID: " + guid + "\n" +
		"\t\tLast attempt @ Sat Aug 30 09:12:44 2026 UTC failed, result 1256 " +
		"(WERR_DS_DRA_ACCESS_DENIED)\n" +
		"\t\t3 consecutive failure(s).\n" +
		"\t\tLast success @ Sat Aug 30 07:00:11 2026 UTC\n\n"

	return head +
		"==== INBOUND NEIGHBORS ====\n\n" +
		ok("DC=lab,DC=example") + failing +
		ok("CN=Schema,CN=Configuration,DC=lab,DC=example") +
		"==== OUTBOUND NEIGHBORS ====\n\n" +
		ok("DC=lab,DC=example") +
		"==== KCC CONNECTION OBJECTS ====\n\n" +
		"Connection --\n" +
		"\tConnection name: 3b2c9a01-77ef-4c05-8a3d-0f6b41cc9e2a\n" +
		"\tEnabled        : TRUE\n" +
		"\tServer DNS name : dc2." + f.realm + "\n"
}

// joinLines renders a list the way samba-tool prints one: one per line.
func joinLines(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.Join(values, "\n") + "\n"
}

// sortedKeys returns the account names in a stable order.
func sortedKeys(users map[string]*fakeUser) []string {
	names := make([]string, 0, len(users))
	for name := range users {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sortedGroupKeys returns the group names in a stable order.
func sortedGroupKeys(groups map[string]*fakeGroup) []string {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// contains reports membership.
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// without returns the slice with one value removed.
func without(values []string, drop string) []string {
	kept := make([]string, 0, len(values))
	for _, v := range values {
		if v != drop {
			kept = append(kept, v)
		}
	}
	return kept
}
