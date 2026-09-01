package samba

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/tui-tools/tui-dc/internal/directory"
)

// osStat restores the real stat after a test that stubbed it.
var osStat = os.Stat

// statOnly returns a stat that finds exactly one path. This file's own
// source is stat'ed in its place, which is a plain file wherever the test
// runs.
func statOnly(path string) func(string) (fs.FileInfo, error) {
	return func(name string) (fs.FileInfo, error) {
		if name == path {
			return os.Stat("provision_test.go")
		}
		return nil, fs.ErrNotExist
	}
}

// showOutput is `samba-tool domain passwordsettings show` as 4.22 prints it.
const showOutput = `Password information for domain 'DC=lab,DC=example'

Password complexity: on
Store plaintext passwords: off
Password history length: 24
Minimum password length: 7
Minimum password age (days): 1
Maximum password age (days): 42
Account lockout duration (mins): 30
Account lockout threshold (attempts): 0
Reset account lockout after (mins): 30
`

func TestParsePasswordSettings(t *testing.T) {
	policy := ParsePasswordSettings(showOutput)
	if !policy.Read {
		t.Fatal("a parsed policy must report itself read")
	}
	if len(policy.Settings) != 9 {
		t.Fatalf("parsed %d settings, want 9: %+v", len(policy.Settings), policy.Settings)
	}
	byName := map[string]string{}
	for _, s := range policy.Settings {
		if s.Name == "" {
			t.Errorf("the line %q did not map to an editable setting", s.Label)
		}
		byName[s.Name] = s.Value
	}
	for name, want := range map[string]string{
		"complexity":     "on",
		"min-pwd-length": "7",
		"max-pwd-age":    "42",
	} {
		if byName[name] != want {
			t.Errorf("%s = %q, want %q", name, byName[name], want)
		}
	}
	// Every editable setting the tool offers must be present in what a real
	// show prints, or the editor offers rows the screen never has.
	for _, field := range directory.PolicyFields {
		if _, ok := byName[field.Name]; !ok {
			t.Errorf("the show output has no line for %s", field.Name)
		}
	}
}

func TestParsePasswordSettingsUnknownLineSurvives(t *testing.T) {
	policy := ParsePasswordSettings("Some future setting: 12\n")
	if len(policy.Settings) != 1 || policy.Settings[0].Name != "" {
		t.Errorf("an unknown line should be kept read-only: %+v", policy.Settings)
	}
}

func TestParseProvisionOutput(t *testing.T) {
	out := "Setting up sam.ldb users and groups\n" +
		"A Kerberos configuration suitable for Samba AD has been generated " +
		"at /var/lib/samba/private/krb5.conf\n" +
		"Once the above files are installed, your Samba AD server will be ready to use\n" +
		"Server Role:           active directory domain controller\n" +
		"Hostname:              dc1\n" +
		"NetBIOS Domain:        LAB\n" +
		"DNS Domain:            lab.example\n" +
		"DOMAIN SID:            S-1-5-21-1-2-3\n" +
		"Admin password:        xoh7aeF9quiZ~ie0Aiph\n"
	result := ParseProvisionOutput(out)
	if result.AdminPassword != "xoh7aeF9quiZ~ie0Aiph" {
		t.Errorf("AdminPassword = %q", result.AdminPassword)
	}
	if result.Krb5Conf != "/var/lib/samba/private/krb5.conf" {
		t.Errorf("Krb5Conf = %q", result.Krb5Conf)
	}
	if len(result.Summary) != 5 {
		t.Errorf("summary = %+v", result.Summary)
	}
}

// TestParseProvisionOutputWithoutPassword covers a provision that was given a
// password by other means: the result must not invent one.
func TestParseProvisionOutputWithoutPassword(t *testing.T) {
	result := ParseProvisionOutput("Server Role:           active directory domain controller\n")
	if result.AdminPassword != "" {
		t.Errorf("AdminPassword = %q, want empty", result.AdminPassword)
	}
}

func TestDetectDCUnit(t *testing.T) {
	defer func() { statFile = osStat }()
	statFile = statOnly("/usr/lib/systemd/system/samba-ad-dc.service")
	if unit, ok := DetectDCUnit(); !ok || unit != "samba-ad-dc.service" {
		t.Errorf("Debian shape: unit = %q ok=%v", unit, ok)
	}
	statFile = statOnly("/usr/lib/systemd/system/samba.service")
	if unit, ok := DetectDCUnit(); !ok || unit != "samba.service" {
		t.Errorf("Arch shape: unit = %q ok=%v", unit, ok)
	}
	statFile = statOnly("")
	if _, ok := DetectDCUnit(); ok {
		t.Error("a machine with no unit file detected one")
	}
}

// TestFreshFakeWalksProvision is the parity test --demo-fresh rests on: a
// machine with no domain, the wizard's exact command, and the same loaders
// and parsers reading the domain it created.
func TestFreshFakeWalksProvision(t *testing.T) {
	ctx := context.Background()
	fake := NewFakeFresh()

	model, err := fake.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !model.Installed || model.Domain.IsDC() || model.Reachable {
		t.Fatalf("a fresh machine loaded as a domain: %+v", model.Domain)
	}
	if model.Policy.Read {
		t.Error("a fresh machine has no password policy to read")
	}

	cmd, err := fake.BuildProvision(directory.Provision{
		Realm: "corp.internal", NetBIOS: "CORP",
		DNSBackend: directory.DNSBackendInternal, Forwarder: "10.0.0.1",
	})
	if err != nil {
		t.Fatalf("BuildProvision: %v", err)
	}
	out, err := fake.Run(ctx, cmd)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	result := ParseProvisionOutput(out)
	if result.AdminPassword == "" {
		t.Error("the fake provision printed no Admin password line to parse")
	}
	if result.Krb5Conf == "" {
		t.Error("the fake provision printed no krb5.conf note to parse")
	}

	model, err = fake.Load(ctx)
	if err != nil {
		t.Fatalf("Load after provision: %v", err)
	}
	if !model.Domain.IsDC() || model.Domain.Realm != "corp.internal" {
		t.Errorf("the provisioned domain did not load: %+v", model.Domain)
	}
	if len(model.Users) == 0 || len(model.Groups) == 0 {
		t.Error("the provisioned domain is empty")
	}
	if !model.Policy.Read {
		t.Error("the provisioned domain's password policy was not read")
	}

	// Provisioning twice is refused at build time, before any dialog opens.
	if _, err := fake.BuildProvision(directory.Provision{
		Realm: "two.example", NetBIOS: "TWO",
		DNSBackend: directory.DNSBackendInternal,
	}); !errors.Is(err, directory.ErrDomainExists) {
		t.Errorf("a second provision built: err = %v", err)
	}
}

// TestProvisionedFakeRefusesProvision is the other half of the same guard on
// the domain --demo starts with.
func TestProvisionedFakeRefusesProvision(t *testing.T) {
	fake := NewFake()
	if _, err := fake.BuildProvision(directory.Provision{
		Realm: "lab.example", NetBIOS: "LAB",
		DNSBackend: directory.DNSBackendInternal,
	}); !errors.Is(err, directory.ErrDomainExists) {
		t.Errorf("provision over the demo domain built: err = %v", err)
	}
}

// TestFakePolicyRoundTrip drives a policy edit the way the app does and reads
// the change back through the parser.
func TestFakePolicyRoundTrip(t *testing.T) {
	ctx := context.Background()
	fake := NewFake()
	spec := directory.ActionSpec{Action: directory.PasswordPolicySet}
	cmd, err := fake.Build(spec, directory.Intent{
		Action: directory.PasswordPolicySet,
		Target: "min-pwd-length", Value: "12",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := fake.Run(ctx, cmd); err != nil {
		t.Fatalf("Run: %v", err)
	}
	model, err := fake.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, setting := range model.Policy.Settings {
		if setting.Name == "min-pwd-length" && setting.Value != "12" {
			t.Errorf("min-pwd-length = %q after setting it to 12", setting.Value)
		}
	}

	// `default` restores Samba's default, as the real command does.
	cmd, err = fake.Build(spec, directory.Intent{
		Action: directory.PasswordPolicySet,
		Target: "min-pwd-length", Value: "default",
	})
	if err != nil {
		t.Fatalf("Build default: %v", err)
	}
	if _, err := fake.Run(ctx, cmd); err != nil {
		t.Fatalf("Run default: %v", err)
	}
	model, _ = fake.Load(ctx)
	for _, setting := range model.Policy.Settings {
		if setting.Name == "min-pwd-length" && setting.Value != "7" {
			t.Errorf("min-pwd-length = %q after resetting to default", setting.Value)
		}
	}
}
