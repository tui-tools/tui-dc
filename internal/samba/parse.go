package samba

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/tui-tools/tui-dc/internal/directory"
)

// This file turns samba-tool's output into the model. It is the part of the
// tool most exposed to bytes nobody here wrote: a domain controller's answer
// is whatever that version of Samba decided to print, on a machine that may
// be mid-upgrade or mid-failure.
//
// So every function here holds the same contract. It never panics, it never
// errors: a line it does not recognise is skipped, and what it did recognise
// is returned. A parser that can fail is a parser that can stop a tool from
// showing the half of the domain it did understand, which is exactly the half
// somebody debugging an outage needs. The fuzz targets in fuzz_test.go assert
// that contract for every parser here.

// ParseDomainInfo reads `samba-tool domain info <server>`, whose output is a
// block of "Label : value" lines.
func ParseDomainInfo(out string) directory.Domain {
	var d directory.Domain
	for _, line := range strings.Split(out, "\n") {
		label, value, ok := splitColon(line)
		if !ok {
			continue
		}
		switch strings.ToLower(label) {
		case "forest":
			d.Forest = value
		case "domain":
			d.Realm = value
		case "netbios domain":
			d.NetBIOS = value
		case "dc name":
			d.DCName = value
		case "dc netbios name":
			d.DCNetBIOS = value
		case "server site":
			d.ServerSite = value
		case "client site":
			d.ClientSite = value
		}
	}
	return d
}

// ParseDomainLevel reads `samba-tool domain level show`, which prints one
// "Something function level: (Windows) 2016" line per level.
func ParseDomainLevel(out string) (forest, domain, lowest string) {
	for _, line := range strings.Split(out, "\n") {
		label, value, ok := splitColon(line)
		if !ok {
			continue
		}
		value = strings.TrimSpace(strings.TrimPrefix(value, "(Windows)"))
		switch {
		case strings.EqualFold(label, "forest function level"):
			forest = value
		case strings.EqualFold(label, "domain function level"):
			domain = value
		case strings.HasPrefix(strings.ToLower(label), "lowest function level"):
			lowest = value
		}
	}
	return forest, domain, lowest
}

// splitColon splits "label : value", tolerating any amount of space around the
// separator and returning false for a line that has none.
func splitColon(line string) (label, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	label = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	if label == "" {
		return "", "", false
	}
	return label, value, true
}

// ParseNameList reads the output of `samba-tool user list`, `group list`,
// `computer list` and `group listmembers`, all of which print one name per
// line and nothing else.
//
// A blank line is skipped, and so is a line that is plainly not a name: those
// versions of samba-tool that print a warning to stdout before the list would
// otherwise put it in the middle of the accounts table.
func ParseNameList(out string) []string {
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || !plausibleName(name) || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// plausibleName rejects the lines a list can contain that are not entries: a
// warning, a usage line, a traceback. A name in Active Directory can contain
// spaces ("Domain Admins"), so the test is not "one word" but "no punctuation
// a message has and a name does not".
func plausibleName(s string) bool {
	if len(s) > 256 {
		return false
	}
	if strings.ContainsAny(s, ":\t") {
		return false
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "#") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// LDIF is one entry as `samba-tool user show` and `computer show` print it:
// attribute names mapped to every value that appeared for them, in order.
type LDIF map[string][]string

// First returns the first value of an attribute, or "".
func (l LDIF) First(name string) string {
	for key, values := range l {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// All returns every value of an attribute.
func (l LDIF) All(name string) []string {
	for key, values := range l {
		if strings.EqualFold(key, name) {
			return values
		}
	}
	return nil
}

// ParseLDIF reads one LDIF entry. It handles the two pieces of the format that
// a naive split on ":" gets wrong: a continuation line, which starts with a
// single space and belongs to the attribute above it, and a base64 value,
// which is marked by a second colon and is decoded when it decodes and kept as
// it was printed when it does not.
//
// Only the first entry is read. `user show` prints exactly one, and an output
// that somehow contains two is a machine doing something this tool should not
// guess about.
func ParseLDIF(out string) LDIF {
	entry := LDIF{}
	var attr string
	var value strings.Builder
	var base64Value bool

	flush := func() {
		if attr == "" {
			return
		}
		text := value.String()
		if base64Value {
			if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text)); err == nil {
				text = string(decoded)
			}
		}
		entry[attr] = append(entry[attr], text)
		attr, base64Value = "", false
		value.Reset()
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			// A blank line closes the entry. Anything after it belongs to
			// another one, which is not this function's business.
			flush()
			if len(entry) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, " ") {
			// A continuation of the attribute above, with the one leading
			// space removed and nothing else touched.
			if attr != "" {
				value.WriteString(line[1:])
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		flush()
		attr = line[:idx]
		rest := line[idx+1:]
		if strings.HasPrefix(rest, ":") {
			base64Value = true
			rest = rest[1:]
		}
		value.WriteString(strings.TrimPrefix(rest, " "))
	}
	flush()
	return entry
}

// ParseUserShow turns one `samba-tool user show` entry into an account.
func ParseUserShow(name, out string) directory.User {
	entry := ParseLDIF(out)
	user := directory.User{
		Name:        firstNonEmpty(entry.First("sAMAccountName"), name),
		DisplayName: entry.First("displayName"),
		Mail:        entry.First("mail"),
		Description: entry.First("description"),
		DN:          entry.First("dn"),
		Detail:      strings.TrimRight(out, "\n"),
	}
	if user.DisplayName == "" {
		given, sn := entry.First("givenName"), entry.First("sn")
		user.DisplayName = strings.TrimSpace(given + " " + sn)
	}
	if uac, err := strconv.Atoi(entry.First("userAccountControl")); err == nil {
		user.UAC = uac
	}
	user.ApplyUAC()
	user.Expires = ntExpiry(entry.First("accountExpires"))
	user.PasswordLast = ntTime(entry.First("pwdLastSet"))
	user.LastLogon = ntTime(entry.First("lastLogon"))
	if user.PasswordLast == "" && entry.First("pwdLastSet") == "0" {
		user.MustChange = true
	}
	for _, dn := range entry.All("memberOf") {
		if rdn := FirstRDN(dn); rdn != "" {
			user.Groups = append(user.Groups, rdn)
		}
	}
	return user
}

// ParseComputerShow turns one `samba-tool computer show` entry into a machine
// account.
func ParseComputerShow(name, out string) directory.Computer {
	entry := ParseLDIF(out)
	computer := directory.Computer{
		Name:        firstNonEmpty(strings.TrimSuffix(entry.First("sAMAccountName"), "$"), name),
		DN:          entry.First("dn"),
		DNSName:     entry.First("dNSHostName"),
		OS:          entry.First("operatingSystem"),
		OSVersion:   entry.First("operatingSystemVersion"),
		Description: entry.First("description"),
		Detail:      strings.TrimRight(out, "\n"),
	}
	if uac, err := strconv.Atoi(entry.First("userAccountControl")); err == nil {
		computer.Disabled = uac&0x0002 != 0
	}
	return computer
}

// FirstRDN returns the value of a distinguished name's leftmost component,
// which is the short name a reader knows a group or an account by.
func FirstRDN(dn string) string {
	dn = strings.TrimSpace(dn)
	if dn == "" {
		return ""
	}
	// Split on the first comma that is not escaped: a name containing one is
	// written "CN=Doe\, Jane,CN=Users,…".
	var out strings.Builder
	escaped := false
	for _, r := range dn {
		switch {
		case escaped:
			out.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == ',':
			return trimRDNPrefix(out.String())
		default:
			out.WriteRune(r)
		}
	}
	return trimRDNPrefix(out.String())
}

// trimRDNPrefix drops the "CN=" from a relative distinguished name.
func trimRDNPrefix(rdn string) string {
	if idx := strings.Index(rdn, "="); idx >= 0 {
		return strings.TrimSpace(rdn[idx+1:])
	}
	return strings.TrimSpace(rdn)
}

// ParseDNSQuery reads `samba-tool dns query <server> <zone> @ ALL`, which
// prints one "Name=…" header per node and an indented line per record under
// it.
//
//	Name=, Records=3, Children=1
//	  A: 10.10.0.10 (flags=600000f0, serial=2, ttl=900)
//	Name=ws01, Records=1, Children=0
//	  A: 10.10.0.21 (flags=f0, serial=5, ttl=900)
//
// The apex prints as an empty name and is returned as "@", which is what the
// user types to address it.
func ParseDNSQuery(out string) []directory.Record {
	var records []directory.Record
	node := ""
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "Name=") {
			node = strings.TrimPrefix(trimmed, "Name=")
			if idx := strings.Index(node, ","); idx >= 0 {
				node = node[:idx]
			}
			node = strings.TrimSpace(node)
			if node == "" {
				node = "@"
			}
			continue
		}
		if node == "" {
			continue
		}
		label, value, ok := splitColon(trimmed)
		if !ok || !directory.KnownRecordType(strings.ToUpper(label)) &&
			!isExtraRecordType(label) {
			continue
		}
		record := directory.Record{Node: node, Type: strings.ToUpper(label)}
		record.Data, record.TTL = splitRecordData(value)
		records = append(records, record)
	}
	return records
}

// extraRecordTypes are the types a zone contains that `samba-tool dns add`
// will not create. They are shown because they are there — a zone missing its
// SOA on screen would be a lie — and the DNS screen refuses to build a delete
// for one.
var extraRecordTypes = []string{"SOA", "SRV"}

// isExtraRecordType reports one of those.
func isExtraRecordType(label string) bool {
	for _, t := range extraRecordTypes {
		if strings.EqualFold(t, label) {
			return true
		}
	}
	return false
}

// splitRecordData separates a record's value from the trailing
// "(flags=…, serial=…, ttl=…)" samba-tool appends to every line, keeping the
// TTL because it is the only part of it a reader uses.
func splitRecordData(value string) (data string, ttl int) {
	idx := strings.LastIndex(value, "(flags=")
	if idx < 0 {
		return strings.TrimSpace(value), 0
	}
	tail := value[idx:]
	data = strings.TrimSpace(value[:idx])
	if pos := strings.Index(tail, "ttl="); pos >= 0 {
		digits := tail[pos+len("ttl="):]
		end := 0
		for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
			end++
		}
		ttl, _ = strconv.Atoi(digits[:end])
	}
	return data, ttl
}

// ParseShowRepl reads `samba-tool drs showrepl` into one row per naming
// context per direction.
//
// The output is three sections under "==== … ====" banners. The two that
// matter are INBOUND and OUTBOUND NEIGHBORS; each holds a naming context DN on
// its own line, then an indented neighbour line, then the attempt and success
// lines. KCC CONNECTION OBJECTS is a different shape and is not a replication
// status, so it ends the parse.
func ParseShowRepl(out string) directory.Replication {
	repl := directory.Replication{}
	direction := ""
	var current *directory.Partition
	partition := ""

	commit := func() {
		if current != nil {
			repl.Partitions = append(repl.Partitions, *current)
			current = nil
		}
	}

	lines := strings.Split(out, "\n")
	for i, raw := range lines {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "====") {
			commit()
			banner := strings.ToUpper(strings.Trim(trimmed, "= "))
			switch {
			case strings.HasPrefix(banner, "INBOUND"):
				// A banner is the proof that this really is showrepl output
				// and not the error samba-tool prints when it cannot reach a
				// controller. Without one, nothing was read.
				repl.Read = true
				direction = "inbound"
			case strings.HasPrefix(banner, "OUTBOUND"):
				repl.Read = true
				direction = "outbound"
			default:
				// KCC connection objects and anything after them are not a
				// replication status.
				direction = ""
			}
			partition = ""
			continue
		}

		// The header, before the first banner: "Site\DC1" then its GUIDs.
		if direction == "" && repl.Name == "" && i < 8 &&
			strings.Contains(trimmed, `\`) && !strings.Contains(trimmed, ":") {
			site, name, _ := strings.Cut(trimmed, `\`)
			repl.Site, repl.Name = site, name
			continue
		}
		if direction == "" {
			if label, value, ok := splitColon(trimmed); ok &&
				strings.EqualFold(label, "DSA object GUID") && repl.GUID == "" {
				repl.GUID = value
			}
			continue
		}

		indented := strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ")
		if !indented {
			// A naming context DN opens a block.
			commit()
			partition = trimmed
			continue
		}
		if partition == "" {
			continue
		}

		switch {
		case strings.Contains(trimmed, " via "):
			commit()
			neighbor, transport, _ := strings.Cut(trimmed, " via ")
			current = &directory.Partition{
				DN:        partition,
				Direction: direction,
				Neighbor:  strings.TrimSpace(neighbor),
				Transport: strings.TrimSpace(transport),
				OK:        true,
			}
		case current == nil:
			// An indented line with no neighbour above it belongs to nothing.
			continue
		case strings.HasPrefix(trimmed, "Last attempt @"):
			current.LastAttempt = betweenAtAndVerb(trimmed)
			if !strings.Contains(trimmed, "was successful") {
				current.OK = false
				current.Detail = trimmed
			}
		case strings.HasPrefix(trimmed, "Last success @"):
			current.LastSuccess = strings.TrimSpace(
				strings.TrimPrefix(trimmed, "Last success @"))
		case strings.Contains(trimmed, "consecutive failure"):
			if n, err := strconv.Atoi(strings.Fields(trimmed)[0]); err == nil {
				current.Failures = n
			}
		}
	}
	commit()
	return repl
}

// betweenAtAndVerb pulls the timestamp out of a "Last attempt @ <when> was
// successful" or "… <when> failed, result …" line.
func betweenAtAndVerb(line string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "Last attempt @"))
	for _, verb := range []string{" was successful", " failed"} {
		if idx := strings.Index(rest, verb); idx >= 0 {
			return strings.TrimSpace(rest[:idx])
		}
	}
	return rest
}

// ParseServerRole reads the effective configuration `testparm -s` prints and
// returns the two facts about this host that are not in the domain: what role
// it plays, and which DNS backend it was provisioned with.
func ParseServerRole(out string) (role, dnsBackend, realm, workgroup string) {
	for _, line := range strings.Split(out, "\n") {
		label, value, ok := splitColon(line)
		if !ok {
			// testparm prints "key = value", not "key: value".
			key, val, found := strings.Cut(line, "=")
			if !found {
				continue
			}
			label, value = strings.TrimSpace(key), strings.TrimSpace(val)
		}
		switch strings.ToLower(label) {
		case "server role":
			role = value
		case "dns forwarder":
			if dnsBackend == "" {
				dnsBackend = "SAMBA_INTERNAL"
			}
		case "server services":
			if strings.Contains(value, "dns") {
				dnsBackend = "SAMBA_INTERNAL"
			}
		case "dcerpc endpoint servers":
			if strings.Contains(value, "dnsserver") && dnsBackend == "" {
				dnsBackend = "SAMBA_INTERNAL"
			}
		case "realm":
			realm = value
		case "workgroup":
			workgroup = value
		}
	}
	return role, dnsBackend, realm, workgroup
}

// ntToUnixSeconds is the gap between the Windows FILETIME epoch
// (1601-01-01 UTC) and the Unix one, in seconds.
const ntToUnixSeconds = 11644473600

// ntNever is the value the directory stores for "this never happens". Windows
// also writes 0 for an account that has never logged in.
const ntNever = int64(9223372036854775807)

// ntTime renders a FILETIME as a date, or "" when it is unset, impossible, or
// not a number at all.
//
// The arithmetic goes through seconds rather than through a time.Duration on
// purpose. A real pwdLastSet is around 1.3e17 hundred-nanosecond intervals,
// which is 1.3e19 nanoseconds — past the end of an int64 Duration. Adding it
// to the epoch silently wraps and produces a date in the wrong millennium,
// which is exactly the kind of wrong a screen renders without complaining.
func ntTime(raw string) string {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n <= 0 || n == ntNever {
		return ""
	}
	seconds := n/10_000_000 - ntToUnixSeconds
	nanoseconds := (n % 10_000_000) * 100
	t := time.Unix(seconds, nanoseconds).UTC()
	if t.Year() < 1601 || t.Year() > 9999 {
		return ""
	}
	return t.Format("2006-01-02 15:04 MST")
}

// ntExpiry renders accountExpires, whose "never" is 0 as well as the maximum.
func ntExpiry(raw string) string {
	if strings.TrimSpace(raw) == "0" {
		return ""
	}
	return ntTime(raw)
}

// firstNonEmpty returns the first value that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
