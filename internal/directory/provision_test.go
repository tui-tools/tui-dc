package directory

import (
	"strings"
	"testing"
)

// TestBuildProvisionCommandArgv spells out the argv the wizard's confirm
// dialog shows. The one thing to notice is what is absent: no --adminpass,
// ever. Left out, samba-tool generates the Administrator password itself and
// prints it once — the same one-time pattern user create --random-password
// uses — so no password exists in an argv or in this process.
func TestBuildProvisionCommandArgv(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Provision
		want string
	}{
		{"internal dns with a forwarder",
			Provision{Realm: "lab.example", NetBIOS: "lab",
				DNSBackend: DNSBackendInternal, Forwarder: "10.0.0.1"},
			"samba-tool domain provision --realm=LAB.EXAMPLE --domain=LAB " +
				"--server-role=dc --dns-backend=SAMBA_INTERNAL --dns-forwarder=10.0.0.1"},
		{"internal dns without a forwarder",
			Provision{Realm: "corp.internal", NetBIOS: "CORP",
				DNSBackend: DNSBackendInternal},
			"samba-tool domain provision --realm=CORP.INTERNAL --domain=CORP " +
				"--server-role=dc --dns-backend=SAMBA_INTERNAL"},
		{"bind9 dlz",
			Provision{Realm: "ad.example.org", NetBIOS: "ADEX",
				DNSBackend: DNSBackendBind9DLZ},
			"samba-tool domain provision --realm=AD.EXAMPLE.ORG --domain=ADEX " +
				"--server-role=dc --dns-backend=BIND9_DLZ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := BuildProvisionCommand(tc.p)
			if err != nil {
				t.Fatalf("BuildProvisionCommand: %v", err)
			}
			if got := cmd.String(); got != tc.want {
				t.Errorf("argv = %q\n want %q", got, tc.want)
			}
			if !cmd.Destructive {
				t.Error("a provision must be painted as destructive")
			}
			if !IsProvisionCommand(cmd) {
				t.Error("IsProvisionCommand does not recognise its own command")
			}
			for _, arg := range cmd.Argv {
				if strings.Contains(strings.ToLower(arg), "pass") {
					t.Errorf("a password-shaped argument reached the argv: %q", arg)
				}
			}
			if cmd.Stdin != "" {
				t.Error("provision writes to stdin, which the preview does not show")
			}
		})
	}
}

// TestBuildProvisionCommandRefusals covers the inputs that must not produce a
// runnable command, injection shapes included. A refusal returns the zero
// Command, as everywhere else in this package.
func TestBuildProvisionCommandRefusals(t *testing.T) {
	valid := Provision{Realm: "lab.example", NetBIOS: "LAB",
		DNSBackend: DNSBackendInternal}
	for _, tc := range []struct {
		name   string
		mutate func(p *Provision)
	}{
		{"no realm", func(p *Provision) { p.Realm = "" }},
		{"realm without a dot", func(p *Provision) { p.Realm = "lab" }},
		{"realm with an empty label", func(p *Provision) { p.Realm = "lab..example" }},
		{"realm with a space", func(p *Provision) { p.Realm = "lab example.com" }},
		{"realm that reads as a flag", func(p *Provision) { p.Realm = "--use-rfc2307.x" }},
		{"realm with a newline", func(p *Provision) { p.Realm = "lab.example\n--x" }},
		{"realm label over 63 chars", func(p *Provision) {
			p.Realm = strings.Repeat("a", 64) + ".example"
		}},
		{"no netbios", func(p *Provision) { p.NetBIOS = "" }},
		{"netbios with a dot", func(p *Provision) { p.NetBIOS = "LAB.EXAMPLE" }},
		{"netbios over 15 chars", func(p *Provision) {
			p.NetBIOS = "ABCDEFGHIJKLMNOP"
		}},
		{"netbios with a shell character", func(p *Provision) { p.NetBIOS = "LAB;id" }},
		{"unknown dns backend", func(p *Provision) { p.DNSBackend = "BIND9_FLATFILE" }},
		{"empty dns backend", func(p *Provision) { p.DNSBackend = "" }},
		{"forwarder that is not an IP", func(p *Provision) { p.Forwarder = "dns.local" }},
		{"forwarder that reads as a flag", func(p *Provision) {
			p.Forwarder = "--interactive"
		}},
		{"forwarder with bind9", func(p *Provision) {
			p.DNSBackend = DNSBackendBind9DLZ
			p.Forwarder = "10.0.0.1"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.mutate(&p)
			cmd, err := BuildProvisionCommand(p)
			if err == nil {
				t.Fatalf("built %q, want a refusal", cmd.String())
			}
			if len(cmd.Argv) != 0 {
				t.Errorf("a refusal returned a runnable command: %+v", cmd)
			}
		})
	}
}

// TestDeriveNetBIOS covers the suggestion the wizard prefills.
func TestDeriveNetBIOS(t *testing.T) {
	for realm, want := range map[string]string{
		"lab.example":                    "LAB",
		"corp.internal":                  "CORP",
		"a-very-long-first-label.exampl": "A-VERY-LONG-FIR",
		"":                               "",
	} {
		if got := DeriveNetBIOS(realm); got != want {
			t.Errorf("DeriveNetBIOS(%q) = %q, want %q", realm, got, want)
		}
	}
}

// TestBuildPolicyCommandArgv spells out the password-policy argv per setting
// kind, and the value shapes each accepts.
func TestBuildPolicyCommandArgv(t *testing.T) {
	spec := specFor(t, PasswordPolicySet)
	for _, tc := range []struct {
		name   string
		intent Intent
		want   string
	}{
		{"a number", Intent{Target: "min-pwd-length", Value: "10"},
			"samba-tool domain passwordsettings set --min-pwd-length=10"},
		{"a default", Intent{Target: "max-pwd-age", Value: "default"},
			"samba-tool domain passwordsettings set --max-pwd-age=default"},
		{"an on/off", Intent{Target: "complexity", Value: "off"},
			"samba-tool domain passwordsettings set --complexity=off"},
		{"zero disables", Intent{Target: "account-lockout-threshold", Value: "0"},
			"samba-tool domain passwordsettings set --account-lockout-threshold=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := BuildCommand(spec, tc.intent)
			if err != nil {
				t.Fatalf("BuildCommand: %v", err)
			}
			if got := cmd.String(); got != tc.want {
				t.Errorf("argv = %q\n want %q", got, tc.want)
			}
			if !cmd.Destructive {
				t.Error("a policy change affects every account and is destructive")
			}
		})
	}
}

// TestBuildPolicyCommandRefusals is the injection guard: the target must be a
// setting from the policy table and the value must be a shape that setting
// takes, so free text can never become a samba-tool flag.
func TestBuildPolicyCommandRefusals(t *testing.T) {
	spec := specFor(t, PasswordPolicySet)
	for _, tc := range []struct {
		name   string
		intent Intent
	}{
		{"no selection", Intent{Value: "10"}},
		{"a setting not in the table", Intent{Target: "adminpass", Value: "x"}},
		{"a flag-shaped setting", Intent{Target: "--complexity", Value: "on"}},
		{"free text as a setting", Intent{Target: "min-pwd-length=1 --complexity",
			Value: "1"}},
		{"a word where a number goes", Intent{Target: "min-pwd-length", Value: "ten"}},
		{"a negative number", Intent{Target: "min-pwd-length", Value: "-1"}},
		{"a number where on/off goes", Intent{Target: "complexity", Value: "7"}},
		{"an empty value", Intent{Target: "complexity", Value: ""}},
		{"a value with a newline", Intent{Target: "complexity", Value: "on\n--x"}},
		{"a value smuggling a flag", Intent{Target: "min-pwd-length",
			Value: "1 --complexity=off"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := BuildCommand(spec, tc.intent)
			if err == nil {
				t.Fatalf("built %q, want a refusal", cmd.String())
			}
			if len(cmd.Argv) != 0 {
				t.Errorf("a refusal returned a runnable command: %+v", cmd)
			}
		})
	}
}

// TestPolicyTableIsConsistent keeps the table and its lookups honest.
func TestPolicyTableIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, field := range PolicyFields {
		if field.Name == "" || field.Label == "" || field.Help == "" {
			t.Errorf("field %+v is missing a part", field)
		}
		if strings.HasPrefix(field.Name, "-") {
			t.Errorf("%q carries its own dashes; the builder adds them", field.Name)
		}
		if seen[field.Name] {
			t.Errorf("%q appears twice", field.Name)
		}
		seen[field.Name] = true
		if got, ok := PolicyFieldByName(field.Name); !ok || got.Label != field.Label {
			t.Errorf("PolicyFieldByName(%q) lost the field", field.Name)
		}
		if got, ok := PolicyFieldByLabel(field.Label); !ok || got.Name != field.Name {
			t.Errorf("PolicyFieldByLabel(%q) lost the field", field.Label)
		}
	}
}
