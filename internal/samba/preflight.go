// What has to be true before `samba-tool domain provision` can run at all, and
// the one step that has to follow it on a build that runs the MIT KDC.
//
// Both were reproduced end to end on Fedora 44 with samba 4.24.6. Neither is a
// guess about how provisioning might fail: each is a fact on disk that decides
// it, and each was read back from a refusal that cost a full wizard run to
// reach.
package samba

import (
	"os"
	"regexp"
	"strings"

	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-kit/pkgmgr"
	"github.com/tui-tools/tui-kit/runner"
)

// smbConfPath is the configuration provision refuses to start beside, and
// smbConfAside is where this tool offers to move it. The `.orig` suffix is the
// one Samba's own documentation uses for exactly this step.
const (
	smbConfPath  = "/etc/samba/smb.conf"
	smbConfAside = smbConfPath + ".orig"
)

// adSchemaDir holds the AD schema .ldf files provision loads into the new
// directory. samba-tool and the schema come from different packages, so a
// machine can have the command and not the data.
const adSchemaDir = "/usr/share/samba/setup/ad-schema"

// detectDistro names the distribution, replaceable in tests. It reads
// /etc/os-release through the kit — the same fact --report prints, so the
// preflight and the bug report cannot disagree about what this machine is.
var detectDistro = pkgmgr.DetectDistro

// ProvisionPreflight reports what stops provisioning on this host, given the
// server role the read already learned through testparm.
//
// Both conditions are cheap and both used to be discovered late: the first
// after the realm had been typed twice and the command confirmed, the second
// most of the way through a provision that then died in a Python traceback.
func ProvisionPreflight(serverRole string) directory.Preflight {
	var preflight directory.Preflight
	if condition, found := smbConfCondition(serverRole); found {
		preflight.Conditions = append(preflight.Conditions, condition)
	}
	if condition, found := adSchemaCondition(); found {
		preflight.Conditions = append(preflight.Conditions, condition)
	}
	return preflight
}

// smbConfCondition is the distribution's own smb.conf standing in the way.
//
// Fedora's `samba` package ships /etc/samba/smb.conf with `security = user`,
// which testparm resolves to `server role = auto`, and provision refuses:
//
//	ERROR(…ProvisioningError): guess_names: 'server role=auto' in
//	/etc/samba/smb.conf must match chosen server role 'active directory domain
//	controller'!  Please remove the smb.conf file and let provision generate it
//
// The rule is wider than Fedora: any smb.conf that resolves to a role other
// than the DC one stops it, which is why the resolved role decides rather than
// the distribution. A host whose smb.conf already says domain controller is not
// this wizard's host at all — the provision offer is refused there anyway.
func smbConfCondition(serverRole string) (directory.PreflightCondition, bool) {
	info, err := statFile(smbConfPath)
	if err != nil || info.IsDir() {
		return directory.PreflightCondition{}, false
	}
	if (directory.Domain{ServerRole: serverRole}).IsDC() {
		return directory.PreflightCondition{}, false
	}
	_, asideErr := statFile(smbConfAside)
	return smbConfAsideCondition(serverRole, asideErr == nil), true
}

// smbConfAsideCondition is the condition's text and its fix, given the role the
// file resolves to and whether the destination of the move already exists. It
// is separate from the stat above so the fake backend can report the same
// condition from its own state — a demo that stat'ed the machine running it
// would show something different on every host.
func smbConfAsideCondition(serverRole string, asideExists bool) directory.PreflightCondition {
	role := strings.TrimSpace(serverRole)
	if role == "" {
		role = "something other than a domain controller"
	}
	condition := directory.PreflightCondition{
		Title: smbConfPath + " configures this host as " + role,
		Detail: []string{
			"provision will not start beside it: it refuses unless the resolved",
			"server role is already the domain controller one, and it writes a",
			"fresh smb.conf itself rather than editing the file it finds.",
		},
	}

	// The destination is never overwritten. A .orig that already exists is
	// somebody's earlier configuration, and a previewed `mv` that silently
	// replaced it would be the one irreversible thing in this tool.
	if asideExists {
		condition.Detail = append(condition.Detail, "",
			smbConfAside+" already exists, so this tool will not move the file:",
			"one previewed command cannot be allowed to overwrite a saved",
			"configuration. Move or remove that file in a shell, then press P again.")
		return condition
	}

	condition.Detail = append(condition.Detail, "",
		"The tool can move it aside — provision then writes its own.")
	condition.Fix = &runner.Command{
		Argv:        []string{"mv", smbConfPath, smbConfAside},
		Description: "Move " + smbConfPath + " aside",
	}
	condition.FixBody = "The file is moved, not deleted: it stays at " +
		smbConfAside + ". But it is also the configuration of whatever this " +
		"Samba serves today — a file server's shares and its password database " +
		"stop being configured the moment it is gone, and what provision writes " +
		"in its place is a domain controller's smb.conf. On a host that is only " +
		"going to be a domain controller that is exactly what you want."
	return condition
}

// adSchemaCondition is the AD provisioning data missing.
//
// With samba-tool present and the schema absent, provision gets most of the way
// in and then raises a bare Python traceback:
//
//	ERROR(<class 'FileNotFoundError'>): uncaught exception - [Errno 2] No such
//	file or directory:
//	'/usr/share/samba/setup/ad-schema/AD_DS_Attributes_Windows_Server_v1903.ldf'
//
// A stat of the directory answers it before anything runs.
func adSchemaCondition() (directory.PreflightCondition, bool) {
	if info, err := statFile(adSchemaDir); err == nil && info.IsDir() {
		return directory.PreflightCondition{}, false
	}
	distro := detectDistro()
	condition := directory.PreflightCondition{
		Title: "the AD provisioning data is not installed",
		Detail: []string{
			"samba-tool is here, but the AD schema it loads is not: " + adSchemaDir,
			"is missing, and provision fails part of the way in with a",
			"FileNotFoundError on one of the .ldf files in it.",
			"",
		},
	}
	if packages, known := provisionPackages(distro); known {
		condition.Detail = append(condition.Detail,
			"On "+distroName(distro)+" it comes from "+packages+".")
	} else {
		condition.Detail = append(condition.Detail,
			"This tool does not know which package carries it on "+
				distroName(distro)+": install whatever your distribution ships",
			"the Samba AD DC in, and the AD DC daemon with it.")
	}
	condition.Detail = append(condition.Detail,
		"Installing packages is not this tool's job, so there is nothing to",
		"confirm here — install them and press P again.")
	return condition, true
}

// provisionPackages names the packages that carry the AD provisioning data and
// the AD DC daemon, per distribution.
//
// Only names that were verified with `dnf provides` on a real Fedora 44 host
// are here, and the list grows only the same way. A guessed package name is
// worse than no package name: it sends a reader to a command that does nothing,
// and it makes the tool sound certain about a distribution nobody tested it on.
// That is the same rule DetectDCUnit follows by looking for the unit file on
// disk instead of mapping a distribution to a unit name.
func provisionPackages(d pkgmgr.Distro) (string, bool) {
	ids := append([]string{d.ID}, d.Like...)
	for _, id := range ids {
		switch strings.ToLower(id) {
		case "fedora", "rhel", "centos":
			return "samba-dc-provision (the schema files) and samba-dc " +
				"(the samba.service AD DC daemon)", true
		}
	}
	return "", false
}

// distroName is how the condition refers to the machine. os-release may be
// unreadable, and "this distribution" is the honest answer then.
func distroName(d pkgmgr.Distro) string {
	if name := strings.TrimSpace(d.String()); name != "" {
		return name
	}
	return "this distribution"
}

// The Kerberos configuration, and where a build that runs the MIT KDC reads it.
const (
	krb5ConfPath   = "/etc/krb5.conf"
	krb5IncludeDir = "/etc/krb5.conf.d"
	krb5DropInPath = krb5IncludeDir + "/samba-dc.conf"
)

// readFile reads a small configuration file, replaceable in tests.
var readFile = os.ReadFile

// defaultRealmRe matches a `default_realm` that is actually in effect. Fedora
// ships /etc/krb5.conf with the line commented out, which is why a comment
// character anywhere before it disqualifies the line.
var defaultRealmRe = regexp.MustCompile(`(?m)^[^#;\n]*\bdefault_realm\s*=\s*\S`)

// absolutePath matches the only shape a generated path may have before it
// becomes an argument. The value is parsed out of samba's own output, and a
// path with a space or a newline in it is not something to hand to a command.
var absolutePath = regexp.MustCompile(`^/[\x21-\x7e]*$`)

// Krb5DropIn is the previewed step that makes a freshly provisioned controller
// start on a samba built against the MIT KDC, which is what Fedora ships.
//
// The chain, verified on Fedora 44: samba runs krb5kdc, krb5kdc reads
// /etc/krb5.conf, and Fedora ships that file with `default_realm` commented
// out. So `systemctl enable --now samba.service` — the step this tool already
// offered — fails, and all the journal says is:
//
//	samba[…]: samba_terminate: …: mitkdc child process exited
//	systemd[1]: samba.service: Main process exited, code=exited, status=1/FAILURE
//
// while the cause is only visible by running `samba -i --debug-stdout -d 3` by
// hand: "krb5kdc: Configuration file does not specify default realm".
//
// Provision generates the right file at /var/lib/samba/private/krb5.conf, and
// where /etc/krb5.conf.d/ exists it drops in without editing anything. This is
// offered only where all three facts hold: the transcript named the generated
// file, the include directory exists, and /etc/krb5.conf sets no default_realm
// of its own. A host that already names a realm gets today's note instead —
// contradicting a realm somebody configured is a merge for a person to do.
func Krb5DropIn(generated string) (runner.Command, bool) {
	generated = strings.TrimSpace(generated)
	if generated == "" || !absolutePath.MatchString(generated) {
		return runner.Command{}, false
	}
	if info, err := statFile(krb5IncludeDir); err != nil || !info.IsDir() {
		return runner.Command{}, false
	}
	if data, err := readFile(krb5ConfPath); err == nil && defaultRealmRe.Match(data) {
		return runner.Command{}, false
	}
	return runner.Command{
		Argv:        []string{"install", "-m", "644", generated, krb5DropInPath},
		Description: "Install the generated Kerberos configuration as " + krb5DropInPath,
	}, true
}

// Krb5DropInBody is what the confirm dialog says about that step. It is here
// rather than in the UI because the reason is a fact about this backend.
const Krb5DropInBody = "On a samba built against the MIT KDC — Fedora's build " +
	"is — the KDC reads /etc/krb5.conf, and a realm it cannot find there makes " +
	"the whole samba unit exit at startup with nothing in the journal but " +
	"\"mitkdc child process exited\". This installs the configuration provision " +
	"generated into the include directory /etc/krb5.conf.d, which changes no " +
	"existing file. It comes before the unit is started, because the unit does " +
	"not start without it."
