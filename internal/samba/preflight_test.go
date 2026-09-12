package samba

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tui-tools/tui-dc/internal/directory"
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

// globSet returns a glob that answers from a fixed set of paths, the way
// filepath.Glob answers from the filesystem. The paths are written the way
// statSet writes them, so one list of strings describes a host to both.
func globSet(paths ...string) func(string) ([]string, error) {
	return func(pattern string) ([]string, error) {
		var matched []string
		for _, path := range paths {
			clean := strings.TrimSuffix(path, "/")
			if ok, err := filepath.Match(pattern, clean); err == nil && ok {
				matched = append(matched, clean)
			}
		}
		return matched, nil
	}
}

// host describes one of the lab guests to both seams at once: what exists on it
// and what its os-release says. Every field of every host below was read off the
// guest itself during the three-distribution matrix run.
type host struct {
	distro  pkgmgr.Distro
	paths   []string
	missing []string // the python modules its interpreter cannot find
}

// apply points the package's seams at this host.
func (h host) apply(t *testing.T) {
	t.Helper()
	restore(t)
	statFile = statSet(h.paths...)
	globPaths = globSet(h.paths...)
	detectDistro = func() pkgmgr.Distro { return h.distro }
	missing := h.missing
	missingPythonModules = func(asked []string) []string {
		var found []string
		for _, module := range asked {
			for _, name := range missing {
				if name == module {
					found = append(found, module)
				}
			}
		}
		return found
	}
}

// fedora44 is the distribution the two conditions were reproduced on.
var fedora44 = pkgmgr.Distro{
	ID: "fedora", VersionID: "44", PrettyName: "Fedora Linux 44 (Workstation Edition)",
}

// ubuntu2404 and omarchy401 are the other two guests of the matrix run. Ubuntu
// answers as Debian through ID_LIKE, and Omarchy Server as Arch — which is the
// whole reason the package table is keyed on the family and not on the ID.
var (
	ubuntu2404 = pkgmgr.Distro{
		ID: "ubuntu", Like: []string{"debian"}, VersionID: "24.04",
		PrettyName: "Ubuntu 24.04.5 LTS",
	}
	omarchy401 = pkgmgr.Distro{
		ID: "omarchy-server", Like: []string{"omarchy", "arch"}, VersionID: "4.0.1",
		PrettyName: "Omarchy Server 4.0.1",
	}
)

// restore puts the package's injection points back after a test.
func restore(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		statFile = osStat
		readFile = os.ReadFile
		detectDistro = pkgmgr.DetectDistro
		globPaths = filepath.Glob
		missingPythonModules = probePythonModules
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

// The three guests as the preflight finds them, in the state `lab.sh dc seed`
// leaves each one in — which is the state a user reaches by installing samba and
// trying to provision a domain.

// ubuntuSeeded is Ubuntu 24.04.5 exactly as `lab.sh dc seed` leaves it —
// `apt-get install --no-install-recommends samba samba-common-bin` — with the
// paths and the probe answer read off that guest. The AD DC unit is there,
// because `samba` carries it; the schema, the ldb modules, the VFS modules and
// winbindd are not, because three of those are only `Recommends` of `samba` and
// winbind only a `Suggests`. Both python modules *are* importable there, and that
// is a fact about the release rather than an oversight: python3-samba depends on
// python3-markdown and samba-common-bin on python3-cryptography.
func ubuntuSeeded() host {
	return host{
		distro: ubuntu2404,
		paths: []string{
			"/etc/samba/smb.conf",
			"/usr/lib/systemd/system/samba-ad-dc.service",
			"/usr/lib/systemd/system/smbd.service",
			"/usr/lib/systemd/system/nmbd.service",
			"/etc/systemd/system/multi-user.target.wants/smbd.service",
			"/etc/systemd/system/multi-user.target.wants/nmbd.service",
			// The multiarch module directory is there; what the recommended
			// packages would have put in it is not.
			"/usr/lib/x86_64-linux-gnu/samba/",
		},
	}
}

// TestPreflightOnUbuntuNamesEveryPackageItNeeds is issue #20 on the guest it was
// found on: one screen, every missing piece, every package named by the name dpkg
// gave for the file that carries it.
func TestPreflightOnUbuntuNamesEveryPackageItNeeds(t *testing.T) {
	ubuntuSeeded().apply(t)

	preflight := ProvisionPreflight("standalone server")
	detail := conditionsText(preflight)
	for _, want := range []string{
		"samba-ad-provision", // the AD schema
		"ldb/samba_secrets.so in samba's module directory", // the fact read
		"vfs/acl_xattr.so in samba's module directory",     //
		"winbindd, which the domain controller forks",      //
		"samba-dsdb-modules samba-vfs-modules winbind",     // one line to install
		"Ubuntu 24.04.5 LTS",                               // and which host this is
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the screen does not name %q:\n%s", want, detail)
		}
	}
	// Neither python module is named, because the interpreter on that guest finds
	// both. A condition for something that is installed would be this tool
	// inventing work for a reader, and it would block the wizard while doing it.
	for _, absent := range []string{"python3-markdown", "python3-cryptography"} {
		if strings.Contains(detail, absent) {
			t.Errorf("a module the interpreter found was reported missing:\n%s", detail)
		}
	}
	// Three conditions, and exactly one of them is this tool's to clear. Three
	// rather than seven is deliberate: see adDCCondition.
	if len(preflight.Conditions) != 3 {
		t.Errorf("conditions = %s", titles(preflight))
	}
	fixable := preflight.Fixable()
	if len(fixable) != 1 ||
		fixable[0].Fix.String() != "mv /etc/samba/smb.conf /etc/samba/smb.conf.orig" {
		t.Errorf("the tool offered to run %+v", fixable)
	}
	// The order is the order a run hits them: the configuration in the way first,
	// the data provision loads second, everything else last.
	if !strings.Contains(preflight.Conditions[0].Title, "/etc/samba/smb.conf") ||
		!strings.Contains(preflight.Conditions[1].Title, "AD provisioning data") ||
		!strings.Contains(preflight.Conditions[2].Title, "beyond samba-tool") {
		t.Errorf("the conditions are out of order: %s", titles(preflight))
	}
	// And it has to fit a terminal: the run that found this screen too tall was a
	// 160x44 pane, and the box lost its title off the top. The longest line is
	// checked too, because the notice wraps rather than cuts and a line that
	// wrapped three times would put the height back.
	lines, longest := 0, 0
	for _, condition := range preflight.Conditions {
		lines += len(condition.Detail) + 2
		for _, line := range condition.Detail {
			if n := len([]rune(line)); n > longest {
				longest = n
			}
		}
	}
	if lines > 38 {
		t.Errorf("the screen is %d lines before the title and the footer", lines)
	}
	if longest > 78 {
		t.Errorf("the longest line is %d characters, which wraps on a narrow screen",
			longest)
	}
}

// TestPreflightOnArchNamesItsTwoPythonPackages is the Arch half of issue #20.
// Everything else on that guest comes from one package, which is why the two
// python modules are the whole finding there — and one of them, cryptography, is
// also why samba-tool cannot run at all (issue #19).
func TestPreflightOnArchNamesItsTwoPythonPackages(t *testing.T) {
	arch := host{
		distro: omarchy401,
		paths: []string{
			// Arch ships no /etc/samba/smb.conf, and `samba` carries the schema,
			// the unit, the modules and winbindd.
			"/usr/share/samba/setup/ad-schema/",
			"/usr/lib/systemd/system/samba.service",
			"/usr/lib/samba/",
			"/usr/lib/samba/ldb/samba_secrets.so",
			"/usr/lib/samba/vfs/acl_xattr.so",
			"/usr/bin/winbindd",
		},
		missing: []string{"cryptography", "markdown"},
	}
	arch.apply(t)

	preflight := ProvisionPreflight("auto")
	if len(preflight.Conditions) != 1 {
		t.Fatalf("conditions = %s", titles(preflight))
	}
	detail := conditionsText(preflight)
	// cryptography first: without it no samba-tool subcommand runs, so nothing
	// below it can even be reached.
	if !strings.Contains(detail, "cryptography module") ||
		strings.Index(detail, "cryptography module") > strings.Index(detail, "markdown module") {
		t.Errorf("cryptography is not the first piece listed:\n%s", detail)
	}
	for _, want := range []string{
		"  python-cryptography python-markdown", "Omarchy Server 4.0.1",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the screen does not name %q:\n%s", want, detail)
		}
	}
	// Arch's names, not Debian's: the distribution is read, never assumed.
	if strings.Contains(detail, "python3-") {
		t.Errorf("a Debian package name reached an Arch host:\n%s", detail)
	}
	// And nothing about the two samba modules or winbindd, which that one package
	// brought: a condition naming a file that is on disk would be a bug.
	for _, absent := range []string{"samba_secrets", "acl_xattr", "winbindd"} {
		if strings.Contains(detail, absent) {
			t.Errorf("%s is on this host and was reported missing:\n%s", absent, detail)
		}
	}
	if len(preflight.Fixable()) != 0 {
		t.Error("the tool offered to install a package")
	}
}

// TestPreflightOnFedoraStaysTheTwoConditionsItAlwaysWas is the gate, and it is
// the reason the screen on the one distribution that already worked did not grow.
//
// In the seed state Fedora has samba and samba-tools and no samba-dc, so there
// is no AD DC unit on disk — and on Fedora every one of the five new conditions
// would be true, because each of those files arrives with samba-dc or with what
// it depends on. Reporting them would be five lines of noise under a condition
// that already says to install samba-dc.
func TestPreflightOnFedoraStaysTheTwoConditionsItAlwaysWas(t *testing.T) {
	fedora := host{
		distro: fedora44,
		// samba's own smb.conf, and nothing of the AD DC.
		paths:   []string{"/etc/samba/smb.conf", "/usr/lib64/samba/"},
		missing: []string{"cryptography", "markdown"},
	}
	fedora.apply(t)

	preflight := ProvisionPreflight("auto")
	if len(preflight.Conditions) != 2 {
		t.Fatalf("conditions = %s", titles(preflight))
	}
	detail := conditionsText(preflight)
	for _, want := range []string{"samba-dc-provision", "samba-dc "} {
		if !strings.Contains(detail, want) {
			t.Errorf("the screen does not name %q:\n%s", want, detail)
		}
	}

	// With samba-dc installed the same host says all of it, and with Fedora's
	// own names: samba-dc carries the ldb modules, samba the VFS ones,
	// samba-winbind winbindd.
	withDC := fedora
	withDC.paths = append([]string{"/usr/lib/systemd/system/samba.service"},
		fedora.paths...)
	withDC.apply(t)
	withDCPreflight := ProvisionPreflight("auto")
	detail = conditionsText(withDCPreflight)
	if len(withDCPreflight.Conditions) != 3 {
		t.Fatalf("conditions = %s", titles(withDCPreflight))
	}
	// Fedora's own names for the same pieces: samba-dc carries the ldb modules,
	// samba-winbind winbindd, and the two python modules are python3-*.
	for _, want := range []string{
		"python3-cryptography", "python3-markdown", "samba-dc", "samba-winbind",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("with the daemon installed the screen does not name %q:\n%s",
				want, detail)
		}
	}
}

// TestPreflightOnAnUnverifiedDistroNamesNoPackage: the honest fallback, for the
// grouped condition as well as for the schema. The facts are still named — a path,
// a module, a binary — and no package is.
func TestPreflightOnAnUnverifiedDistroNamesNoPackage(t *testing.T) {
	unknown := ubuntuSeeded()
	unknown.distro = pkgmgr.Distro{ID: "void", PrettyName: "Void Linux"}
	// A host missing both python modules as well, which none of the guests is: the
	// wording is what is under test here.
	unknown.missing = []string{"cryptography", "markdown"}
	unknown.apply(t)

	preflight := ProvisionPreflight("standalone server")
	detail := conditionsText(preflight)
	if !strings.Contains(detail, "This tool does not know which package carries it on") {
		t.Errorf("the schema condition named a package on an unverified distro:\n%s", detail)
	}
	if !strings.Contains(detail, "This tool does not know what these are called on") {
		t.Errorf("the grouped condition named packages on an unverified distro:\n%s", detail)
	}
	for _, guess := range []string{"samba-ad-provision", "samba-dsdb-modules",
		"python3-markdown", "python-markdown", "samba-winbind"} {
		if strings.Contains(detail, guess) {
			t.Errorf("a package name was guessed on an unverified distribution: %q", guess)
		}
	}
	if !strings.Contains(detail, "Void Linux") {
		t.Errorf("the screen does not say which distribution it does not know:\n%s", detail)
	}
	// The facts are still named, which is the point of saying anything at all.
	for _, want := range []string{
		"ldb/samba_secrets.so", "vfs/acl_xattr.so", "winbindd",
		"the python markdown module", "the python cryptography module",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the screen does not name what it looked for, %q:\n%s", want, detail)
		}
	}
}

// TestGroupedConditionNamesEachDistributionsPackages is the per-distribution
// naming on its own: the same host state, three distributions, three sets of
// names — and on Ubuntu 24.04.5 the python half cannot happen at all, because its
// samba packages depend on both modules, so that row is the Debian names as this
// tool would print them rather than a state any guest was in.
func TestGroupedConditionNamesEachDistributionsPackages(t *testing.T) {
	for _, tc := range []struct {
		distro pkgmgr.Distro
		unit   string
		want   string
	}{
		{omarchy401, "/usr/lib/systemd/system/samba.service",
			"python-cryptography python-markdown ldb samba"},
		{ubuntu2404, "/usr/lib/systemd/system/samba-ad-dc.service",
			"python3-cryptography python3-markdown samba-dsdb-modules " +
				"samba-vfs-modules winbind"},
		{fedora44, "/usr/lib/systemd/system/samba.service",
			"python3-cryptography python3-markdown samba-dc samba samba-winbind"},
	} {
		t.Run(tc.distro.ID, func(t *testing.T) {
			// A host with the AD DC unit, samba's module directory, and nothing else
			// a provision needs.
			host{
				distro:  tc.distro,
				paths:   []string{tc.unit, "/usr/lib64/samba/", "/usr/lib/samba/"},
				missing: []string{"cryptography", "markdown"},
			}.apply(t)

			condition, found := adDCCondition()
			if !found {
				t.Fatal("a host missing all five pieces reported nothing")
			}
			detail := strings.Join(condition.Detail, "\n")
			// The list is read as one line of names however it had to wrap: where
			// it breaks is the screen's business, which names are on it is this
			// test's.
			flat := strings.Join(strings.Fields(
				detail[strings.Index(detail, "they come from:"):]), " ")
			if !strings.HasPrefix(flat, "they come from: "+tc.want+" Installing") {
				t.Errorf("the packages named are not %q:\n%s", tc.want, detail)
			}
			if condition.Fix != nil {
				t.Error("the tool offered to install a package")
			}
		})
	}
}

// TestSambaModuleMissingProvesAbsenceOnlyWhereThereIsADirectory is the rule the
// check has to follow to be allowed to block a wizard: it reports a module
// missing from a directory it found, and reports nothing about a host whose samba
// keeps its modules somewhere this tool has never seen.
func TestSambaModuleMissingProvesAbsenceOnlyWhereThereIsADirectory(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		want  bool
	}{
		{"ubuntu, multiarch, module absent",
			[]string{"/usr/lib/x86_64-linux-gnu/samba/"}, true},
		{"ubuntu, multiarch, module present", []string{
			"/usr/lib/x86_64-linux-gnu/samba/",
			"/usr/lib/x86_64-linux-gnu/samba/ldb/samba_secrets.so"}, false},
		{"fedora, lib64, module present", []string{
			"/usr/lib64/samba/", "/usr/lib64/samba/ldb/samba_secrets.so"}, false},
		{"arch, both layouts, present in one", []string{
			"/usr/lib/samba/", "/usr/lib64/samba/",
			"/usr/lib/samba/ldb/samba_secrets.so"}, false},
		{"no samba module directory at all", []string{"/etc/samba/smb.conf"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore(t)
			statFile = statSet(tc.paths...)
			globPaths = globSet(tc.paths...)
			if got := sambaModuleMissing("ldb/samba_secrets.so"); got != tc.want {
				t.Errorf("sambaModuleMissing = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFileServerDisableIsOneStepNamingOnlyWhatIsEnabled is issue #18's step: the
// units it offers are the ones that are both installed and enabled, it is one
// command, and on a host with nothing enabled there is no step at all.
func TestFileServerDisableIsOneStepNamingOnlyWhatIsEnabled(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"ubuntu as samba leaves it: both installed and enabled", []string{
			"/usr/lib/systemd/system/smbd.service",
			"/usr/lib/systemd/system/nmbd.service",
			"/etc/systemd/system/multi-user.target.wants/smbd.service",
			"/etc/systemd/system/multi-user.target.wants/nmbd.service",
		}, "systemctl disable --now smbd.service nmbd.service"},
		{"debian with nmbd already disabled", []string{
			"/lib/systemd/system/smbd.service",
			"/lib/systemd/system/nmbd.service",
			"/etc/systemd/system/multi-user.target.wants/smbd.service",
		}, "systemctl disable --now smbd.service"},
		{"installed and enabled by nothing", []string{
			"/usr/lib/systemd/system/smbd.service",
			"/usr/lib/systemd/system/nmbd.service",
		}, ""},
		{"a .wants link with no unit behind it", []string{
			"/etc/systemd/system/multi-user.target.wants/smbd.service",
		}, ""},
		{"fedora and arch, which ship neither unit", []string{
			"/usr/lib/systemd/system/samba.service",
			"/etc/systemd/system/multi-user.target.wants/samba.service",
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore(t)
			statFile = statSet(tc.paths...)
			globPaths = globSet(tc.paths...)
			cmd, offered := FileServerDisable()
			if tc.want == "" {
				if offered {
					t.Fatalf("a step was offered: %q", cmd.String())
				}
				return
			}
			if !offered {
				t.Fatal("no step was offered")
			}
			if got := cmd.String(); got != tc.want {
				t.Errorf("the step is %q\n want %q", got, tc.want)
			}
			if !cmd.Destructive {
				t.Error("stopping a file server was not marked as a change to weigh")
			}
			if cmd.Description == "" {
				t.Error("a previewed step with no description")
			}
		})
	}
}

// TestWinbinddFoundInEitherLocation: Ubuntu ships winbindd only in /usr/sbin,
// Fedora and Arch ship it in both, and either answer means the controller will
// find the child it forks.
func TestWinbinddFoundInEitherLocation(t *testing.T) {
	for _, path := range winbinddPaths {
		restore(t)
		statFile = statSet(path)
		if winbinddMissing() {
			t.Errorf("winbindd at %s was reported missing", path)
		}
	}
	restore(t)
	statFile = statSet("/etc/samba/smb.conf")
	if !winbinddMissing() {
		t.Error("a host with no winbindd anywhere was reported as having one")
	}
}

// TestParsePythonProbeReadsOnlyWhatItAsked is the probe's output, and the one
// thing the parser has to refuse: a line the interpreter printed that is not one
// of the names the question carried. A warning on stderr turned into a package
// name would be this tool telling somebody to install a sentence.
func TestParsePythonProbeReadsOnlyWhatItAsked(t *testing.T) {
	asked := []string{"cryptography", "markdown"}
	captured := read(t, "python-probe-missing.txt")
	if got := ParsePythonProbe(captured, asked); len(got) != 1 || got[0] != "markdown" {
		t.Errorf("the captured probe answered %v, want [markdown]", got)
	}
	noisy := "ImportWarning: something about the interpreter\nmarkdown\ncryptography\n"
	got := ParsePythonProbe(noisy, asked)
	if len(got) != 2 || got[0] != "cryptography" || got[1] != "markdown" {
		t.Errorf("missing = %v, want the two asked names in the order asked", got)
	}
	if len(ParsePythonProbe("", asked)) != 0 {
		t.Error("an interpreter that printed nothing reported a missing module")
	}
}

// titles and conditionsText render a Preflight for an assertion's failure
// message: the screen a reader would see, rather than a Go value.
func titles(p directory.Preflight) string {
	var names []string
	for _, condition := range p.Conditions {
		names = append(names, condition.Title)
	}
	return strings.Join(names, " | ")
}

func conditionsText(p directory.Preflight) string {
	var lines []string
	for _, condition := range p.Conditions {
		lines = append(lines, condition.Title)
		lines = append(lines, condition.Detail...)
	}
	return strings.Join(lines, "\n")
}
