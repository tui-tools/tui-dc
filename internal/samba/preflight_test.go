package samba

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/tui-tools/tui-kit/pkgmgr"
)

// statSet returns a stat that finds exactly the paths given, each as the kind
// it is written as: a name ending in "/" is a directory, anything else a plain
// file. The real filesystem stands in for both — this file's own source for a
// file, its directory for a directory — so the test does not have to invent an
// fs.FileInfo.
func statSet(paths ...string) func(string) (fs.FileInfo, error) {
	return func(name string) (fs.FileInfo, error) {
		for _, path := range paths {
			if strings.TrimSuffix(path, "/") != name {
				continue
			}
			if strings.HasSuffix(path, "/") {
				return os.Stat(".")
			}
			return os.Stat("preflight_test.go")
		}
		return nil, fs.ErrNotExist
	}
}

// fedora44 is the distribution the two conditions were reproduced on.
var fedora44 = pkgmgr.Distro{
	ID: "fedora", VersionID: "44", PrettyName: "Fedora Linux 44 (Workstation Edition)",
}

// restore puts the package's injection points back after a test.
func restore(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		statFile = osStat
		readFile = os.ReadFile
		detectDistro = pkgmgr.DetectDistro
	})
}

// TestProvisionPreflightBothConditions is a Fedora host with the samba package
// and without samba-dc-provision: both conditions, in the order a provision
// hits them, the first with its previewed command and the second without one.
func TestProvisionPreflightBothConditions(t *testing.T) {
	restore(t)
	statFile = statSet("/etc/samba/smb.conf")
	detectDistro = func() pkgmgr.Distro { return fedora44 }

	preflight := ProvisionPreflight("auto")
	if preflight.OK() || len(preflight.Conditions) != 2 {
		t.Fatalf("conditions = %+v", preflight.Conditions)
	}
	if !strings.Contains(preflight.Conditions[0].Title, "/etc/samba/smb.conf") {
		t.Errorf("the first condition is %q", preflight.Conditions[0].Title)
	}
	if !strings.Contains(preflight.Conditions[1].Title, "AD provisioning data") {
		t.Errorf("the second condition is %q", preflight.Conditions[1].Title)
	}

	fixable := preflight.Fixable()
	if len(fixable) != 1 {
		t.Fatalf("fixable = %+v", fixable)
	}
	if got := fixable[0].Fix.String(); got !=
		"mv /etc/samba/smb.conf /etc/samba/smb.conf.orig" {
		t.Errorf("the offered command is %q", got)
	}
	if fixable[0].FixBody == "" {
		t.Error("a previewed fix with no explanation of what it costs")
	}
	// The packages are named, and only because this pair was verified with
	// `dnf provides` on Fedora 44.
	detail := strings.Join(preflight.Conditions[1].Detail, "\n")
	for _, want := range []string{"samba-dc-provision", "samba-dc "} {
		if !strings.Contains(detail, want) {
			t.Errorf("the missing-package condition does not name %q:\n%s", want, detail)
		}
	}
	if preflight.Conditions[1].Fix != nil {
		t.Error("the tool offered to install a package")
	}
}

// TestProvisionPreflightSmbConfAlone: the schema is installed, the
// distribution's smb.conf is not out of the way.
func TestProvisionPreflightSmbConfAlone(t *testing.T) {
	restore(t)
	statFile = statSet("/etc/samba/smb.conf", adSchemaDir+"/")
	detectDistro = func() pkgmgr.Distro { return fedora44 }

	preflight := ProvisionPreflight("standalone server")
	if len(preflight.Conditions) != 1 {
		t.Fatalf("conditions = %+v", preflight.Conditions)
	}
	if !strings.Contains(preflight.Conditions[0].Title, "standalone server") {
		t.Errorf("the condition does not name the role it read: %q",
			preflight.Conditions[0].Title)
	}
	if len(preflight.Fixable()) != 1 {
		t.Error("the move was not offered")
	}
}

// TestProvisionPreflightSchemaAlone: no smb.conf at all, which is the state
// provision wants, and the schema missing.
func TestProvisionPreflightSchemaAlone(t *testing.T) {
	restore(t)
	statFile = statSet()
	detectDistro = func() pkgmgr.Distro { return fedora44 }

	preflight := ProvisionPreflight("auto")
	if len(preflight.Conditions) != 1 {
		t.Fatalf("conditions = %+v", preflight.Conditions)
	}
	if !strings.Contains(preflight.Conditions[0].Title, "AD provisioning data") {
		t.Errorf("the condition is %q", preflight.Conditions[0].Title)
	}
	if len(preflight.Fixable()) != 0 {
		t.Error("something was offered for a missing package")
	}
}

// TestProvisionPreflightClearHostOpensTheWizard is the case that matters most:
// a host where nothing is wrong must not be made to read a screen.
func TestProvisionPreflightClearHostOpensTheWizard(t *testing.T) {
	restore(t)
	statFile = statSet(adSchemaDir + "/")
	detectDistro = func() pkgmgr.Distro { return fedora44 }

	if preflight := ProvisionPreflight("auto"); !preflight.OK() {
		t.Errorf("a clear host was blocked: %+v", preflight.Conditions)
	}
	// And a host that is already a controller has no condition either, whatever
	// its smb.conf says — the provision offer is refused there for other
	// reasons, and the preflight must not claim to be the one refusing.
	statFile = statSet("/etc/samba/smb.conf", adSchemaDir+"/")
	if preflight := ProvisionPreflight(
		"active directory domain controller"); !preflight.OK() {
		t.Errorf("a controller was given a preflight condition: %+v",
			preflight.Conditions)
	}
}

// TestProvisionPreflightKeepsAnExistingOrig: the destination of the move is
// never overwritten, and the condition says so instead of offering a command.
func TestProvisionPreflightKeepsAnExistingOrig(t *testing.T) {
	restore(t)
	statFile = statSet("/etc/samba/smb.conf", smbConfAside, adSchemaDir+"/")
	detectDistro = func() pkgmgr.Distro { return fedora44 }

	preflight := ProvisionPreflight("auto")
	if len(preflight.Conditions) != 1 {
		t.Fatalf("conditions = %+v", preflight.Conditions)
	}
	if len(preflight.Fixable()) != 0 {
		t.Fatal("the tool offered to overwrite an existing smb.conf.orig")
	}
	if detail := strings.Join(preflight.Conditions[0].Detail, "\n"); !strings.Contains(
		detail, smbConfAside+" already exists") {
		t.Errorf("the condition does not say why nothing is offered:\n%s", detail)
	}
}

// TestProvisionPreflightUnknownDistroNamesNoPackage: on a distribution whose
// package names were never verified, the path is named and the guess is not
// made. A wrong package name is worse than none.
func TestProvisionPreflightUnknownDistroNamesNoPackage(t *testing.T) {
	restore(t)
	statFile = statSet()
	detectDistro = func() pkgmgr.Distro {
		return pkgmgr.Distro{ID: "someos", PrettyName: "SomeOS Linux"}
	}

	preflight := ProvisionPreflight("auto")
	detail := strings.Join(preflight.Conditions[0].Detail, "\n")
	if !strings.Contains(detail, adSchemaDir) {
		t.Errorf("the condition does not name the missing path:\n%s", detail)
	}
	if !strings.Contains(detail, "does not know which package") {
		t.Errorf("the condition does not admit what it does not know:\n%s", detail)
	}
	if strings.Contains(detail, "samba-dc-provision") {
		t.Errorf("a Fedora package name leaked onto another distribution:\n%s", detail)
	}
}

// TestKrb5DropInOffered is the Fedora shape: an include directory, and an
// /etc/krb5.conf whose default_realm is commented out, which is what makes the
// samba unit fail to start until the generated file is in place.
func TestKrb5DropInOffered(t *testing.T) {
	restore(t)
	statFile = statSet(krb5IncludeDir+"/", krb5ConfPath)
	readFile = func(string) ([]byte, error) {
		return []byte("[libdefaults]\n    dns_lookup_realm = false\n" +
			"#    default_realm = EXAMPLE.COM\n" +
			"    includedir /etc/krb5.conf.d/\n"), nil
	}

	cmd, ok := Krb5DropIn("/var/lib/samba/private/krb5.conf")
	if !ok {
		t.Fatal("the drop-in was not offered where it is needed")
	}
	want := "install -m 644 /var/lib/samba/private/krb5.conf " +
		"/etc/krb5.conf.d/samba-dc.conf"
	if cmd.String() != want {
		t.Errorf("the offered command is %q\n want %q", cmd.String(), want)
	}
}

// TestKrb5DropInRefusals: every reason not to offer it.
func TestKrb5DropInRefusals(t *testing.T) {
	restore(t)
	readFile = func(string) ([]byte, error) { return nil, fs.ErrNotExist }

	// No include directory: the generated file is a merge for a person to do,
	// which is what the note on the result screen says.
	statFile = statSet(krb5ConfPath)
	if _, ok := Krb5DropIn("/var/lib/samba/private/krb5.conf"); ok {
		t.Error("a host with no /etc/krb5.conf.d was offered a drop-in")
	}

	// A realm already configured: dropping a second one in could contradict it.
	statFile = statSet(krb5IncludeDir+"/", krb5ConfPath)
	readFile = func(string) ([]byte, error) {
		return []byte("[libdefaults]\n    default_realm = OTHER.EXAMPLE\n"), nil
	}
	if _, ok := Krb5DropIn("/var/lib/samba/private/krb5.conf"); ok {
		t.Error("a host that already sets default_realm was offered a drop-in")
	}

	// Nothing to install: the transcript never named a generated file.
	readFile = func(string) ([]byte, error) { return nil, fs.ErrNotExist }
	if _, ok := Krb5DropIn(""); ok {
		t.Error("a drop-in was offered with no file to install")
	}
	// And a path that is not one is refused rather than handed to a command.
	for _, bad := range []string{"krb5.conf", "/var/lib/samba/private/krb 5.conf"} {
		if _, ok := Krb5DropIn(bad); ok {
			t.Errorf("%q reached an argv", bad)
		}
	}
}

// TestParseProvisionWarnings holds the two WARNING lines the result screen
// exists to repeat: the address samba chose for itself, and the absent IPv6.
func TestParseProvisionWarnings(t *testing.T) {
	out := "INFO 2026-01-01 10:00:00,104 pid:1234 " +
		"/usr/lib64/python3.14/site-packages/samba/provision/__init__.py " +
		"#2180: Looking up IPv4 addresses\n" +
		"WARNING 2026-01-01 10:00:00,106 pid:1234 " +
		"/usr/lib64/python3.14/site-packages/samba/provision/__init__.py " +
		"#2204: No IPv6 address will be assigned\n" +
		"WARNING 2026-01-01 10:00:00,107 pid:1234 " +
		"/usr/lib64/python3.14/site-packages/samba/provision/__init__.py " +
		"#2122: More than one IPv4 address found. Using 192.168.10.20\n"

	result := ParseProvisionOutput(out)
	want := []string{
		"No IPv6 address will be assigned",
		"More than one IPv4 address found. Using 192.168.10.20",
	}
	if len(result.Warnings) != len(want) {
		t.Fatalf("warnings = %q", result.Warnings)
	}
	for i, line := range want {
		if result.Warnings[i] != line {
			t.Errorf("warning %d is %q\n want %q", i, result.Warnings[i], line)
		}
	}
	// A warning is not a summary line and must not be mistaken for one.
	if len(result.Summary) != 0 {
		t.Errorf("summary = %q", result.Summary)
	}
}

// TestParseProvisionWarningsFromTheCapturedRefusal reads the real thing: the
// transcript of a provision that died on the missing AD schema, on a host with
// several IPv4 addresses. Both warnings are in it, and neither was ever shown
// to the user before the result screen repeated them.
func TestParseProvisionWarningsFromTheCapturedRefusal(t *testing.T) {
	result := ParseProvisionOutput(read(t, "domain-provision-schema-missing.txt"))
	if len(result.Warnings) != 2 {
		t.Fatalf("warnings = %q", result.Warnings)
	}
	if !strings.Contains(result.Warnings[1], "More than one IPv4 address found") {
		t.Errorf("warnings = %q", result.Warnings)
	}
	// A refused provision prints no password and no summary, which is exactly
	// why a failure needs its transcript rather than a parsed result.
	if result.AdminPassword != "" || len(result.Summary) != 0 {
		t.Errorf("a failed provision parsed as a successful one: %+v", result)
	}

	// The earlier refusal prints even less: it never gets as far as a warning,
	// and the reason is one line in the middle of a Python traceback.
	refused := read(t, "domain-provision-role-refused.txt")
	result = ParseProvisionOutput(refused)
	if result.AdminPassword != "" || len(result.Summary) != 0 ||
		len(result.Warnings) != 0 {
		t.Errorf("the role refusal parsed as something: %+v", result)
	}
	if !strings.Contains(refused, "must match chosen server role") {
		t.Error("the captured refusal is not the one the preflight is about")
	}
}

// TestParseProvisionWarningsBareShape: older samba prints the level and the
// message with nothing between them.
func TestParseProvisionWarningsBareShape(t *testing.T) {
	result := ParseProvisionOutput(
		"WARNING: No IPv6 address will be assigned\n" +
			"Setting up share.ldb\n" +
			"this line mentions WARNING in passing\n")
	if len(result.Warnings) != 1 ||
		result.Warnings[0] != "No IPv6 address will be assigned" {
		t.Errorf("warnings = %q", result.Warnings)
	}
}
