package directory

import (
	"fmt"
	"net"
	"strings"

	"github.com/tui-tools/tui-kit/runner"
)

// Provision is everything the wizard collects to create a new domain. It is
// deliberately small: the rest of `samba-tool domain provision`'s many knobs
// keep their defaults, which are the defaults every Samba guide assumes.
type Provision struct {
	// Realm is the domain's DNS name (lab.example). samba-tool uppercases it
	// into the Kerberos realm itself; the command carries it uppercased so
	// the preview reads the way the realm will.
	Realm string
	// NetBIOS is the pre-2000 short name (LAB), at most 15 characters.
	NetBIOS string
	// DNSBackend is SAMBA_INTERNAL or BIND9_DLZ.
	DNSBackend string
	// Forwarder is an optional DNS forwarder IP, only meaningful with the
	// internal DNS server. Empty means none.
	Forwarder string
}

// The DNS backends the wizard offers. samba-tool also knows BIND9_FLATFILE
// (deprecated) and NONE; neither is something to steer a new domain into.
const (
	DNSBackendInternal = "SAMBA_INTERNAL"
	DNSBackendBind9DLZ = "BIND9_DLZ"
)

// DNSBackends returns the backends the wizard's picker offers, default first.
func DNSBackends() []string {
	return []string{DNSBackendInternal, DNSBackendBind9DLZ}
}

// ErrDomainExists reports a provision asked for on a host that already serves
// a domain. Provisioning over an existing domain is not a change, it is a
// replacement, and this tool refuses to build the command at all.
var ErrDomainExists = fmt.Errorf(
	"this host already serves a domain: provisioning over it is refused")

// ValidateRealm checks a realm the way DNS reads one: dot-separated labels of
// letters, digits and hyphens, at least two of them, none empty, none starting
// or ending with a hyphen. The error names the rule that was broken, because a
// wizard that says "invalid" teaches nothing.
func ValidateRealm(realm string) error {
	realm = strings.TrimSpace(realm)
	if realm == "" {
		return fmt.Errorf("a realm is required — the domain's DNS name, like lab.example")
	}
	if len(realm) > 253 {
		return fmt.Errorf("a DNS name is at most 253 characters")
	}
	labels := strings.Split(realm, ".")
	if len(labels) < 2 {
		return fmt.Errorf(
			"a realm is a fully qualified DNS name with at least one dot, like lab.example")
	}
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("%q has an empty label — two dots in a row, or a leading or trailing dot", realm)
		}
		if len(label) > 63 {
			return fmt.Errorf("the label %q is longer than 63 characters", label)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("the label %q starts or ends with a hyphen", label)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
				(r < '0' || r > '9') && r != '-' {
				return fmt.Errorf(
					"the label %q contains %q — a DNS label is letters, digits and hyphens",
					label, string(r))
			}
		}
	}
	return nil
}

// ValidateNetBIOS checks the short domain name: 1 to 15 characters, letters,
// digits and hyphens, no dot — a NetBIOS name with a dot in it is the classic
// provision mistake, because it looks like the realm and is not.
func ValidateNetBIOS(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a NetBIOS domain name is required — the short name, like LAB")
	}
	if len(name) > 15 {
		return fmt.Errorf("a NetBIOS name is at most 15 characters, and %q is %d",
			name, len(name))
	}
	if strings.Contains(name, ".") {
		return fmt.Errorf(
			"a NetBIOS name has no dot — that is the realm; the short name is its first label")
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != '-' {
			return fmt.Errorf(
				"%q contains %q — a NetBIOS name is letters, digits and hyphens",
				name, string(r))
		}
	}
	return nil
}

// ValidateForwarder checks the optional DNS forwarder: empty, or one IP
// address. It is refused rather than passed through because this value lands
// in smb.conf, where a typo is silent until the first lookup fails.
func ValidateForwarder(ip string) error {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return nil
	}
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("%q is not an IP address", ip)
	}
	return nil
}

// DeriveNetBIOS suggests the short name a realm implies: its first label,
// uppercased and clipped to 15 characters. It is a suggestion for the wizard's
// prompt, not a rule — the user can type over it.
func DeriveNetBIOS(realm string) string {
	label, _, _ := strings.Cut(strings.TrimSpace(realm), ".")
	label = strings.ToUpper(label)
	if len(label) > 15 {
		label = label[:15]
	}
	return label
}

// Validate checks the whole of what the wizard collected.
func (p Provision) Validate() error {
	if err := ValidateRealm(p.Realm); err != nil {
		return err
	}
	if err := ValidateNetBIOS(p.NetBIOS); err != nil {
		return err
	}
	backendOK := false
	for _, b := range DNSBackends() {
		if b == p.DNSBackend {
			backendOK = true
		}
	}
	if !backendOK {
		return fmt.Errorf("%q is not a DNS backend this tool provisions", p.DNSBackend)
	}
	if err := ValidateForwarder(p.Forwarder); err != nil {
		return err
	}
	if p.Forwarder != "" && p.DNSBackend != DNSBackendInternal {
		return fmt.Errorf("a DNS forwarder only means something to %s", DNSBackendInternal)
	}
	return nil
}

// BuildProvisionCommand turns what the wizard collected into the exact argv
// that will run. Like BuildCommand it is the only place this command line is
// assembled, it is shared by the real and the fake backend, and a refusal
// returns the zero Command.
//
// One thing is deliberately absent: `--adminpass`. Left out, samba-tool
// generates a strong random Administrator password itself and prints
// `Admin password: …` exactly once when provisioning finishes — the same
// pattern this tool already relies on for `user create --random-password`. So
// no password ever exists in an argv, in this process, or anywhere but that
// one line of samba-tool's own output, which the result screen shows once.
func BuildProvisionCommand(p Provision) (runner.Command, error) {
	p.Realm = strings.ToUpper(strings.TrimSpace(p.Realm))
	p.NetBIOS = strings.ToUpper(strings.TrimSpace(p.NetBIOS))
	p.DNSBackend = strings.TrimSpace(p.DNSBackend)
	p.Forwarder = strings.TrimSpace(p.Forwarder)
	if err := p.Validate(); err != nil {
		return runner.Command{}, err
	}
	argv := []string{Bin, "domain", "provision",
		"--realm=" + p.Realm,
		"--domain=" + p.NetBIOS,
		"--server-role=dc",
		"--dns-backend=" + p.DNSBackend,
	}
	if p.Forwarder != "" {
		argv = append(argv, "--dns-forwarder="+p.Forwarder)
	}
	return runner.Command{
		Argv:        argv,
		Description: "Provision the domain " + strings.ToLower(p.Realm),
		Destructive: true,
	}, nil
}

// IsProvisionCommand reports whether a command is the provision built above.
// The app uses it to give provisioning its longer timeout and its result
// screen without carrying extra state beside the command.
func IsProvisionCommand(cmd runner.Command) bool {
	return len(cmd.Argv) >= 3 && cmd.Argv[0] == Bin &&
		cmd.Argv[1] == "domain" && cmd.Argv[2] == "provision"
}
