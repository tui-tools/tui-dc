// Package directory is the part of tui-dc that is about its own subject: an
// Active Directory domain as `samba-tool` describes it, the actions the tool
// offers on it, and the interface a backend satisfies.
//
// Nothing here starts a process. The model, the action table and the one
// function that turns an intent into an argv all live on this side of the
// boundary, so the command a user confirms can be reasoned about — and fuzzed
// — without a domain controller anywhere near.
package directory

import (
	"sort"
	"strings"
)

// Domain is what a domain controller says about the domain it serves: the
// names, the site, the functional levels and the role this host plays in it.
type Domain struct {
	// Forest and Realm are the DNS names, NetBIOS the pre-2000 short name.
	Forest  string `json:"forest,omitempty"`
	Realm   string `json:"realm,omitempty"`
	NetBIOS string `json:"netbios,omitempty"`

	// DCName is the controller that answered, DCNetBIOS its short name.
	DCName    string `json:"dcName,omitempty"`
	DCNetBIOS string `json:"dcNetbios,omitempty"`

	// ServerSite is the site the answering controller is in, ClientSite the
	// site this host was placed in by the same query. They differ on a
	// machine that is not itself a controller.
	ServerSite string `json:"serverSite,omitempty"`
	ClientSite string `json:"clientSite,omitempty"`

	// The functional levels, as `samba-tool domain level show` prints them.
	ForestLevel string `json:"forestLevel,omitempty"`
	DomainLevel string `json:"domainLevel,omitempty"`
	LowestLevel string `json:"lowestDCLevel,omitempty"`

	// ServerRole and DNSBackend come from this host's own configuration, not
	// from the domain: they answer "what is this machine", which is a
	// different question from "what is the domain".
	ServerRole string `json:"serverRole,omitempty"`
	DNSBackend string `json:"dnsBackend,omitempty"`
}

// IsDC reports whether this host's configuration says it is a domain
// controller. A machine can reach a domain without being one.
func (d Domain) IsDC() bool {
	return strings.Contains(strings.ToLower(d.ServerRole), "domain controller")
}

// User is one account in the directory.
type User struct {
	Name string `json:"name"`
	// DisplayName, Given and Surname are the human-readable names.
	DisplayName string `json:"displayName,omitempty"`
	Mail        string `json:"mail,omitempty"`
	Description string `json:"description,omitempty"`
	// DN is the distinguished name, which is also where the account lives in
	// the tree — an account in an OU is worth seeing as such.
	DN string `json:"dn,omitempty"`
	// UAC is the userAccountControl bit field, and the two booleans below are
	// the bits of it a reader actually asks about.
	UAC        int  `json:"userAccountControl,omitempty"`
	Disabled   bool `json:"disabled"`
	NoExpiry   bool `json:"passwordNeverExpires"`
	Locked     bool `json:"locked"`
	MustChange bool `json:"mustChangePassword"`
	// Expires is accountExpires rendered, empty when the account never does.
	Expires string `json:"expires,omitempty"`
	// LastLogon and PasswordLast are rendered timestamps, when known.
	LastLogon    string `json:"lastLogon,omitempty"`
	PasswordLast string `json:"passwordLastSet,omitempty"`
	// Groups are the memberships read off the account, short names only.
	Groups []string `json:"groups,omitempty"`
	// Detail is the raw LDIF the account screen shows, when it was read.
	Detail string `json:"-"`
}

// The userAccountControl bits this tool reads. They are named here rather than
// written as numbers at the point of use, because a bit field with no names is
// how a tool starts lying about what it shows.
const (
	uacDisabled         = 0x0002
	uacLockout          = 0x0010
	uacPasswordNoExpiry = 0x10000
)

// ApplyUAC fills the booleans a userAccountControl value implies.
func (u *User) ApplyUAC() {
	u.Disabled = u.UAC&uacDisabled != 0
	u.Locked = u.UAC&uacLockout != 0
	u.NoExpiry = u.UAC&uacPasswordNoExpiry != 0
}

// State is the one word the accounts table shows.
func (u User) State() string {
	switch {
	case u.Disabled:
		return "disabled"
	case u.Locked:
		return "locked"
	default:
		return "enabled"
	}
}

// Group is one group, with the members that were read for it.
type Group struct {
	Name        string   `json:"name"`
	DN          string   `json:"dn,omitempty"`
	Description string   `json:"description,omitempty"`
	Members     []string `json:"members,omitempty"`
	// MembersRead records that the member list was actually asked for, so an
	// empty group and an unread one do not look the same.
	MembersRead bool `json:"membersRead"`
}

// Computer is one machine account.
type Computer struct {
	Name string `json:"name"`
	DN   string `json:"dn,omitempty"`
	// DNSName is dNSHostName, OS the operatingSystem string.
	DNSName     string `json:"dnsName,omitempty"`
	OS          string `json:"os,omitempty"`
	OSVersion   string `json:"osVersion,omitempty"`
	Disabled    bool   `json:"disabled"`
	Description string `json:"description,omitempty"`
	Detail      string `json:"-"`
}

// Record is one DNS record in the domain zone.
type Record struct {
	// Node is the name within the zone; "@" is the zone apex.
	Node string `json:"node"`
	Type string `json:"type"`
	Data string `json:"data"`
	TTL  int    `json:"ttl,omitempty"`
}

// Zone is the domain's own DNS zone, as `samba-tool dns query` returns it.
type Zone struct {
	Name    string   `json:"name,omitempty"`
	Records []Record `json:"records,omitempty"`
	// Read records that the query ran; a zone nobody could query is not an
	// empty zone.
	Read bool `json:"read"`
}

// Partition is one naming context's replication status, in one direction.
type Partition struct {
	// DN is the naming context, Neighbor the controller replicated with.
	DN       string `json:"dn"`
	Neighbor string `json:"neighbor,omitempty"`
	// Direction is "inbound" or "outbound".
	Direction string `json:"direction"`
	// Transport is what showrepl prints after "via", usually RPC.
	Transport string `json:"transport,omitempty"`
	// LastAttempt and LastSuccess are the rendered timestamps, and OK whether
	// the last attempt succeeded.
	LastAttempt string `json:"lastAttempt,omitempty"`
	LastSuccess string `json:"lastSuccess,omitempty"`
	OK          bool   `json:"ok"`
	// Failures is the consecutive failure count showrepl reports.
	Failures int `json:"failures"`
	// Detail is the failure line, when the last attempt did not succeed.
	Detail string `json:"detail,omitempty"`
}

// Replication is the whole of `samba-tool drs showrepl`, parsed.
type Replication struct {
	// Site and Name identify the controller that answered.
	Site string `json:"site,omitempty"`
	Name string `json:"name,omitempty"`
	// GUID is the DSA object GUID.
	GUID       string      `json:"guid,omitempty"`
	Partitions []Partition `json:"partitions,omitempty"`
	// Read records that showrepl ran and was parsed.
	Read bool `json:"read"`
	// Detail is why it could not be read, when it could not.
	Detail string `json:"detail,omitempty"`
}

// OK reports whether every partition's last attempt succeeded. A controller
// with no partitions at all has nothing to be wrong, so it is not "ok": the
// caller distinguishes that with Read.
func (r Replication) OK() bool {
	for _, p := range r.Partitions {
		if !p.OK {
			return false
		}
	}
	return r.Read
}

// Model is everything one read of the domain produced.
type Model struct {
	// Installed reports that samba-tool is on this machine at all. False is a
	// perfectly normal machine, and Detail says so.
	Installed bool   `json:"installed"`
	Detail    string `json:"detail,omitempty"`
	// Version is what `samba-tool --version` printed.
	Version string `json:"version,omitempty"`
	// Reachable reports that a domain controller answered `domain info`.
	Reachable bool `json:"reachable"`

	Domain    Domain         `json:"domain"`
	Policy    PasswordPolicy `json:"passwordPolicy"`
	Users     []User         `json:"users,omitempty"`
	Groups    []Group        `json:"groups,omitempty"`
	Computers []Computer     `json:"computers,omitempty"`
	Zone      Zone           `json:"zone"`
	Repl      Replication    `json:"replication"`

	// Notes are the one-line reasons a part of the read did not happen. They
	// are collected rather than fatal: a machine where `drs showrepl` is
	// refused still has users worth showing.
	Notes []string `json:"notes,omitempty"`
}

// Counts is the summary the header and --check both want.
type Counts struct {
	Users     int `json:"users"`
	Disabled  int `json:"disabledUsers"`
	Groups    int `json:"groups"`
	Computers int `json:"computers"`
	Records   int `json:"records"`
	// Partitions is how many naming contexts showrepl reported, and Failing
	// how many of them had a failed last attempt.
	Partitions int `json:"partitions"`
	Failing    int `json:"failingPartitions"`
}

// Count summarises the model.
func (m Model) Count() Counts {
	c := Counts{
		Users:      len(m.Users),
		Groups:     len(m.Groups),
		Computers:  len(m.Computers),
		Records:    len(m.Zone.Records),
		Partitions: len(m.Repl.Partitions),
	}
	for _, u := range m.Users {
		if u.Disabled {
			c.Disabled++
		}
	}
	for _, p := range m.Repl.Partitions {
		if !p.OK {
			c.Failing++
		}
	}
	return c
}

// SortUsers puts what a reader looks for first on top: the accounts that are
// not usable, then the rest by name.
func SortUsers(users []User) {
	sort.SliceStable(users, func(i, j int) bool {
		a, b := users[i], users[j]
		if a.Disabled != b.Disabled {
			return a.Disabled
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

// SortGroups orders groups by name.
func SortGroups(groups []Group) {
	sort.SliceStable(groups, func(i, j int) bool {
		return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name)
	})
}

// SortComputers orders machine accounts by name.
func SortComputers(computers []Computer) {
	sort.SliceStable(computers, func(i, j int) bool {
		return strings.ToLower(computers[i].Name) < strings.ToLower(computers[j].Name)
	})
}

// SortRecords orders the zone the way the zone reads: the apex first, then by
// node name, then by type.
func SortRecords(records []Record) {
	sort.SliceStable(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if (a.Node == "@") != (b.Node == "@") {
			return a.Node == "@"
		}
		if !strings.EqualFold(a.Node, b.Node) {
			return strings.ToLower(a.Node) < strings.ToLower(b.Node)
		}
		return a.Type < b.Type
	})
}
