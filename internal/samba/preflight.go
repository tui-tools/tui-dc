// What has to be true before `samba-tool domain provision` can run at all, and
// the steps that have to follow it on the host it ran on.
//
// Nothing here is a guess about how provisioning might fail: every condition is
// a fact on disk (or one 27ms question to python) that decides it, and every one
// was read back from a refusal that cost a full wizard run to reach.
//
// The first two conditions and the Kerberos step were reproduced end to end on
// Fedora 44 with samba 4.24.6. The rest come from the three-distribution matrix
// run that followed — Fedora 44, Ubuntu 24.04.5 and Omarchy Server 4.0.1 (Arch)
// — where Fedora turned out to be the easy one: on Debian and Ubuntu five of the
// pieces a provision needs are only `Recommends` or `Suggests` of `samba`, and
// on Arch two python modules its samba package does not depend on are missing.
// Each was found by a provision that ran for minutes and then died, and each is
// a fact that was on disk before it started.
package samba

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
// Every condition is cheap and every one of them used to be discovered late:
// the first after the realm had been typed twice and the command confirmed, the
// rest somewhere inside a provision that had already been writing a directory
// for minutes.
//
// The order is the order a run hits them, so a reader clearing them top to
// bottom never has to come back: the configuration in the way, then the data
// provision loads, then everything else it or the controller after it loads —
// which is one condition with a list in it, because five paragraphs did not fit
// on a terminal. See adDCCondition.
func ProvisionPreflight(serverRole string) directory.Preflight {
	var preflight directory.Preflight
	if condition, found := smbConfCondition(serverRole); found {
		preflight.Conditions = append(preflight.Conditions, condition)
	}
	if condition, found := adSchemaCondition(); found {
		preflight.Conditions = append(preflight.Conditions, condition)
	}
	if condition, found := adDCCondition(); found {
		preflight.Conditions = append(preflight.Conditions, condition)
	}
	return preflight
}

// adDCCondition is the one condition that carries everything a provision and the
// controller need beyond samba-tool and the schema, and nothing when this host
// has all of it.
//
// It is one condition with a list in it rather than one condition per missing
// piece, and the reason is the screen: five separate conditions, each with its
// own paragraph and its own package sentence, made a notice seventy lines long
// that no 44-row terminal could show — the lab run that found this lost the
// title off the top of the box. One grouped condition says the same facts in a
// third of the height, and it puts the package names a reader has to install on
// one line instead of five.
//
// It is reported only on a host that already has an AD DC unit file, and that
// gate is what keeps it honest rather than noisy. On Fedora and on Arch every one
// of these pieces arrives with the AD DC package itself — verified with `rpm -qf`
// on Fedora 44 and `pacman -Qoq` on Omarchy Server 4.0.1 — so on a host that has
// not installed that package yet they would all be missing at once, listed under
// the condition that already says to install it. On Debian and Ubuntu the AD DC
// unit comes from `samba`, which such a host has installed before it ever looks
// for samba-tool, so the gate is open exactly where the gaps are real: there
// three of these are only `Recommends` of `samba` and winbindd's package is only
// a `Suggests`.
func adDCCondition() (directory.PreflightCondition, bool) {
	if _, installed := DetectDCUnit(); !installed {
		return directory.PreflightCondition{}, false
	}
	missing := missingPieces()
	if len(missing) == 0 {
		return directory.PreflightCondition{}, false
	}
	distro := detectDistro()
	condition := directory.PreflightCondition{
		Title: "this host is missing what a provision needs beyond samba-tool",
		Detail: []string{
			"samba-tool is here; what provision loads while it writes the",
			"directory, or what the controller forks afterwards, is not. Each of",
			"these was found by a provision that ran for minutes and then died:",
			"",
		},
	}
	packages, known := packagesFor(distro)
	var names []string
	for _, piece := range missing {
		// The fact first, then what its absence costs — indented under it, so the
		// list reads as a list even where a line has to wrap.
		condition.Detail = append(condition.Detail, "  "+piece.what)
		for _, line := range piece.cost {
			condition.Detail = append(condition.Detail, "      "+line)
		}
		// contains lives in fake.go and is the package's own membership test. Two
		// pieces can come from one package — on Arch nearly all of them do — and a
		// reader told to install the same name twice would wonder what they missed.
		if name := piece.pkg(packages); known && name != "" && !contains(names, name) {
			names = append(names, name)
		}
	}
	condition.Detail = append(condition.Detail, "")
	if len(names) > 0 {
		// The names on their own line, indented and space-separated: it is the
		// list a reader has to hand to their package manager, and one line they
		// can copy is the whole point of grouping these conditions.
		condition.Detail = append(condition.Detail,
			"On "+distroName(distro)+" they come from:")
		for _, line := range wrapWords(strings.Join(names, " "), detailWidth-2) {
			condition.Detail = append(condition.Detail, "  "+line)
		}
	} else {
		condition.Detail = append(condition.Detail,
			"This tool does not know what these are called on "+distroName(distro)+":",
			"install whatever your distribution ships them in.")
	}
	condition.Detail = append(condition.Detail,
		"Installing packages is not this tool's job: install them and press P again.")
	return condition, true
}

// missingPiece is one thing a provision or the controller loads: how the screen
// names it, what its absence costs in the words the transcript used, and which
// package carries it on a given distribution.
type missingPiece struct {
	what string
	cost []string
	pkg  func(distroPackages) string
}

// missingPieces returns the pieces this host does not have, in the order a run
// hits them: the python module samba-tool itself needs first, then what
// provision loads, then the child the controller forks once it is running.
func missingPieces() []missingPiece {
	var missing []missingPiece
	absent := missingPythonModules(pythonModules)
	for _, piece := range adDCPieces {
		if piece.absent(absent) {
			missing = append(missing, piece.missingPiece)
		}
	}
	return missing
}

// adDCPieces is the whole list, each with the fact that decides it. Every one of
// them is read off the filesystem or out of one 27ms question to python — never
// out of a package manager, because the preflight has to answer on a host where
// nothing can be escalated and has to answer before a screen is drawn.
var adDCPieces = []struct {
	missingPiece
	// absent decides it, given the python modules the interpreter could not
	// find (one probe answers for all of them, so it is passed in).
	absent func(absentModules []string) bool
}{
	{
		// Issue #19's cause on Arch: samba-tool builds its own subcommand table
		// through cryptography, so without it nothing runs — not provision, and
		// not the --version this tool read to decide samba-tool is here.
		missingPiece: missingPiece{
			what: "the python cryptography module",
			cost: []string{"no samba-tool subcommand runs at all"},
			pkg:  func(p distroPackages) string { return p.cryptography },
		},
		absent: func(absent []string) bool { return contains(absent, "cryptography") },
	},
	{
		// samba's forest update imports markdown. Reached on Arch, whose samba
		// package depends on neither python module; on Ubuntu 24.04.5
		// python3-samba depends on python3-markdown, so it cannot be missing
		// there — which the probe says for itself rather than this table
		// deciding it per distribution.
		missingPiece: missingPiece{
			what: "the python markdown module",
			cost: []string{
				"provision dies minutes in, in forest_update.py:",
				"ModuleNotFoundError: No module named 'markdown'",
			},
			pkg: func(p distroPackages) string { return p.markdown },
		},
		absent: func(absent []string) bool { return contains(absent, "markdown") },
	},
	{
		missingPiece: missingPiece{
			what: "ldb/samba_secrets.so in samba's module directory",
			cost: []string{
				"provision reaches secrets.ldb, says \"Module [samba_secrets] not",
				"found\", then dies on a bare \"'NoneType' … 'startswith'\" traceback",
			},
			pkg: func(p distroPackages) string { return p.dsdb },
		},
		absent: func([]string) bool { return sambaModuleMissing("ldb/samba_secrets.so") },
	},
	{
		missingPiece: missingPiece{
			what: "vfs/acl_xattr.so in samba's module directory",
			cost: []string{
				"provision loads it to set the ACL on sysvol: \"Error loading",
				"module …/vfs/acl_xattr.so\", then \"smbd_vfs_init failed\"",
			},
			pkg: func(p distroPackages) string { return p.vfs },
		},
		absent: func([]string) bool { return sambaModuleMissing("vfs/acl_xattr.so") },
	},
	{
		// A samba running as an AD DC forks winbindd unconditionally, and a
		// missing binary is not something it survives. This is on the preflight
		// rather than on the result screen because that is where it is cheap: it
		// is true before the wizard asks for a realm, and the alternative is
		// learning it after a provision has spent minutes building a directory
		// the controller then cannot serve.
		missingPiece: missingPiece{
			what: "winbindd, which the domain controller forks",
			cost: []string{
				"the provision succeeds and nothing serves the domain: the unit",
				"dies the second it starts, on \"winbindd: Failed to exec child\"",
			},
			pkg: func(p distroPackages) string { return p.winbind },
		},
		absent: func([]string) bool { return winbinddMissing() },
	},
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
			"samba-tool is here and the AD schema it loads is not:",
			adSchemaDir + " is missing, and provision fails part of",
			"the way in with a FileNotFoundError on one of the .ldf files in it.",
			"",
		},
	}
	if packages, known := provisionPackages(distro); known {
		condition.Detail = append(condition.Detail,
			wrapWords("On "+distroName(distro)+" it comes from "+packages+".",
				detailWidth)...)
	} else {
		condition.Detail = append(condition.Detail,
			"This tool does not know which package carries it on "+
				distroName(distro)+": install whatever your distribution ships",
			"the Samba AD DC in, and the AD DC daemon with it.")
	}
	condition.Detail = append(condition.Detail,
		"Installing packages is not this tool's job: install them and press P again.")
	return condition, true
}

// distroPackages names, per distribution family, the packages that carry what a
// provision and the controller after it need.
//
// Every name in this table was read off one of the three guests of the lab
// matrix, with that distribution's own question about the file itself rather
// than from documentation or memory: `rpm -qf` on Fedora 44, `dpkg -S` on
// Ubuntu 24.04.5, `pacman -Qoq` on Omarchy Server 4.0.1. A distribution that is
// not in the table gets no package name at all, which is the rule the first
// version of this file set and the reason it named only Fedora: a guessed
// package name is worse than no package name, because it sends a reader to a
// command that does nothing and makes the tool sound certain about a
// distribution nobody tested it on. That is the same rule DetectDCUnit follows
// by looking for the unit file on disk instead of mapping a distribution to a
// unit name.
type distroPackages struct {
	// schema carries /usr/share/samba/setup/ad-schema, the .ldf files provision
	// loads into the new directory.
	schema string
	// daemon carries the AD DC unit, and unit is what that unit is called.
	daemon, unit string
	// dsdb carries the ldb modules provision writes the directory through.
	dsdb string
	// vfs carries the VFS modules provision loads to set the sysvol ACL.
	vfs string
	// markdown and cryptography carry the two python modules samba imports.
	markdown, cryptography string
	// winbind carries winbindd, which the AD DC forks.
	winbind string
}

// distroPackageTable is that table, keyed by the os-release ID or ID_LIKE the
// distribution reports.
//
// Fedora splits Samba six ways, Debian and Ubuntu eight, and Arch ships nearly
// all of it in one package — which is why the same missing file has three
// different answers and none of them can be derived from the others.
var distroPackageTable = map[string]distroPackages{
	// Fedora 44 (Cloud Edition), samba 4.24.6. samba-tool itself comes from
	// samba-tools here, and samba-dc depends on samba-winbind and on
	// python3-samba-dc (which requires python3-markdown), so in practice a host
	// that installed the daemon has all of these already.
	"fedora": {
		schema: "samba-dc-provision", daemon: "samba-dc", unit: "samba.service",
		dsdb: "samba-dc", vfs: "samba",
		markdown: "python3-markdown", cryptography: "python3-cryptography",
		winbind: "samba-winbind",
	},
	// Ubuntu 24.04.5 LTS, samba 4.19.5-Ubuntu. samba-tool comes from
	// samba-common-bin; `samba` carries the samba-ad-dc.service unit and only
	// *recommends* samba-ad-provision, samba-dsdb-modules and samba-vfs-modules,
	// and only *suggests* winbind — so all four are absent from a host installed
	// with --no-install-recommends, and winbind from a stock one. The two python
	// modules are named here for completeness rather than from a failure: on
	// 24.04.5 python3-samba *depends* on python3-markdown and samba-common-bin on
	// python3-cryptography, so a host that has samba-tool at all has both, and the
	// probe finds them (verified with apt-cache on the lab guest). The markdown
	// failure in issue #20 is Arch's, where nothing depends on it.
	"debian": {
		schema: "samba-ad-provision", daemon: "samba", unit: "samba-ad-dc.service",
		dsdb: "samba-dsdb-modules", vfs: "samba-vfs-modules",
		markdown: "python3-markdown", cryptography: "python3-cryptography",
		winbind: "winbind",
	},
	// Omarchy Server 4.0.1 (ID_LIKE "omarchy arch"), samba 4.23.x. One package
	// carries samba-tool, the AD schema, the samba.service unit, the VFS modules
	// and winbindd; the ldb modules come from `ldb`, which samba depends on. The
	// two python modules are the gap: `samba` depends on neither, and without
	// python-cryptography samba-tool cannot run at all.
	"arch": {
		schema: "samba", daemon: "samba", unit: "samba.service",
		dsdb: "ldb", vfs: "samba",
		markdown: "python-markdown", cryptography: "python-cryptography",
		winbind: "samba",
	},
}

// packagesFor returns this distribution's names, or false where the table has
// none. ID_LIKE is consulted after ID, which is what makes Ubuntu answer as
// Debian and Omarchy Server answer as Arch without either being named.
func packagesFor(d pkgmgr.Distro) (distroPackages, bool) {
	for _, id := range append([]string{d.ID}, d.Like...) {
		if packages, known := distroPackageTable[strings.ToLower(id)]; known {
			return packages, true
		}
	}
	return distroPackages{}, false
}

// provisionPackages names the packages that carry the AD provisioning data and
// the AD DC daemon, as one phrase for the condition that reports them missing.
//
// On Arch the two are the same package, and saying so once is the truth: a
// reader told to install "samba (the schema files) and samba (the daemon)" would
// reasonably wonder which of the two they had.
func provisionPackages(d pkgmgr.Distro) (string, bool) {
	packages, known := packagesFor(d)
	if !known {
		return "", false
	}
	if packages.schema == packages.daemon {
		return packages.schema + " (the schema files and the " + packages.unit +
			" AD DC daemon)", true
	}
	return packages.schema + " (the schema files) and " + packages.daemon +
		" (the " + packages.unit + " AD DC daemon)", true
}

// detailWidth is how wide a line of a condition may be before it is wrapped.
//
// It is not a guess about terminals in general: the notice screen renders every
// line to the width of its box and wraps rather than cuts, so a long line costs
// height, and height is what a preflight screen ran out of on a 160x44 pane
// during the Ubuntu lab run. 78 leaves room for the box and its padding on an
// 80-column terminal.
const detailWidth = 78

// wrapWords breaks a sentence into lines no wider than width, on spaces. A word
// longer than the whole width is left alone rather than cut: it is a path or a
// package name, and half of either is worse than a long line. Leading space is
// not preserved — an indent is applied to the result, not passed through it.
func wrapWords(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// distroName is how the condition refers to the machine. os-release may be
// unreadable, and "this distribution" is the honest answer then.
func distroName(d pkgmgr.Distro) string {
	if name := strings.TrimSpace(d.String()); name != "" {
		return name
	}
	return "this distribution"
}

// The python modules samba imports, in the order a run needs them, with what
// each one costs when it is absent.
//
// Both are facts about samba rather than about a distribution: samba-tool builds
// its subcommand table through cryptography, and provision's forest update
// imports markdown. So both are checked everywhere and only the package name
// differs.
var pythonModules = []string{"cryptography", "markdown"}

// pythonInterpreter is the interpreter the probe asks, and it is the interpreter
// samba-tool imports through: samba-tool's shebang is `#!/usr/bin/python3` on
// all three guests of the lab matrix, so the question "can python3 import this"
// is the same question samba-tool will answer with a traceback later.
const pythonInterpreter = "python3"

// pythonProbe prints the names it could not find, one per line. find_spec is
// asked rather than the module imported, because importing it would run it: the
// question is whether samba-tool's import will resolve, not what the module does
// when it does. An exception counts as missing — a module that cannot even be
// located without raising is not one samba-tool will import either.
const pythonProbe = `import importlib.util, sys
for name in sys.argv[1:]:
    try:
        found = importlib.util.find_spec(name) is not None
    except Exception:
        found = False
    if not found:
        print(name)
`

// pythonProbeTimeout bounds the probe. It is generous by two orders of
// magnitude on purpose: the measured cost of this question is 27ms on each of
// the three lab guests (python 3.12 on Ubuntu, 3.14 on Fedora and Arch), and a
// preflight that a pathological host could hang is worse than one that
// occasionally gives up.
const pythonProbeTimeout = 3 * time.Second

// missingPythonModules is the probe, replaceable in tests.
var missingPythonModules = probePythonModules

// probePythonModules returns the modules the interpreter cannot find.
//
// This is the one check here that is not a stat, and it is a process rather than
// a look at a directory for a reason: where a python module lives is the
// interpreter's business. The three lab guests put the same two modules in three
// different places — /usr/lib/python3/dist-packages on Ubuntu,
// /usr/lib64/python3.14/site-packages and /usr/lib/python3.14/site-packages on
// Fedora, /usr/lib/python3.14/site-packages on Arch — and a tool that globbed
// for the ones it knew would both miss a layout it had not seen and, worse,
// claim a module was absent that a PYTHONPATH or a venv makes importable. A
// false condition here is not noise, it blocks the wizard, so the authority is
// asked instead of imitated.
//
// It reads and runs nothing of the user's: the argv is built here, the script is
// a constant, it needs no escalation, and it is bounded. Where there is no
// python3 to ask, nothing is reported — absence cannot be proved without an
// interpreter, and a guess is not worth a blocked wizard.
func probePythonModules(modules []string) []string {
	probe, err := runner.New(runner.Options{
		Bin:             pythonInterpreter,
		SearchPaths:     []string{"/usr/bin/python3", "/bin/python3"},
		Timeout:         pythonProbeTimeout,
		PrivilegedReads: new(bool), // false: an import check is nobody's secret
	})
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), pythonProbeTimeout)
	defer cancel()
	out, err := probe.Read(ctx, append([]string{pythonInterpreter, "-c", pythonProbe},
		modules...)...)
	if err != nil {
		// An interpreter that fails to answer has proved nothing, and a
		// condition raised on that would block the wizard on this tool's own
		// failure rather than on the host's state.
		return nil
	}
	return ParsePythonProbe(out, modules)
}

// ParsePythonProbe reads the probe's output: the names it printed, kept in the
// order they were asked for and filtered to the names that were asked. The
// filter is the whole reason this is a function — a probe that printed a
// warning, or an interpreter that greeted the terminal first, must not turn a
// line of its own into a package this tool tells somebody to install.
func ParsePythonProbe(out string, asked []string) []string {
	printed := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		printed[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, module := range asked {
		if printed[module] {
			missing = append(missing, module)
		}
	}
	return missing
}

// sambaModuleDirs is where samba keeps its loadable modules, per layout. The
// three are not guesses: /usr/lib64/samba on Fedora 44, /usr/lib/samba on
// Omarchy Server 4.0.1, and the multiarch /usr/lib/<triplet>/samba on Ubuntu
// 24.04.5 (x86_64-linux-gnu there), each read off the guest itself. The triplet
// is matched rather than derived, because deriving it from GOARCH would be this
// tool guessing at dpkg's naming on an architecture nobody ran it on.
var sambaModuleDirs = []string{
	"/usr/lib64/samba",
	"/usr/lib/samba",
	"/usr/lib/*-linux-gnu/samba",
}

// globPaths expands one of those patterns, replaceable in tests.
var globPaths = filepath.Glob

// sambaModuleMissing reports that samba's module directory is on this host and
// the named module is not in it.
//
// What it proves is exactly that, which is what matters: the file provision
// dlopens is either there or it is not, and no package manager has to be asked.
// Where no module directory can be found at all it reports nothing — a host
// whose samba keeps its modules somewhere this tool has never seen has not
// proved anything to the contrary, and a false condition here would block the
// wizard.
func sambaModuleMissing(module string) bool {
	found := false
	for _, pattern := range sambaModuleDirs {
		matches, err := globPaths(pattern)
		if err != nil {
			continue
		}
		for _, dir := range matches {
			if info, err := statFile(dir); err != nil || !info.IsDir() {
				continue
			}
			found = true
			if info, err := statFile(dir + "/" + module); err == nil && !info.IsDir() {
				return false
			}
		}
	}
	return found
}

// winbinddPaths is where the AD DC looks for the child it forks. Both are
// checked because the two distributions that ship it in /usr/bin also ship it in
// /usr/sbin, and Ubuntu ships it only in /usr/sbin — which is also the path its
// journal names when it is not there.
var winbinddPaths = []string{"/usr/sbin/winbindd", "/usr/bin/winbindd"}

// winbinddMissing reports that neither path has it. What it proves is that the
// controller will not find the child it forks, which is the whole question: on
// stock Ubuntu 24.04, where winbind is only a `Suggests` of samba,
// `systemctl enable --now samba-ad-dc.service` fails on a provision that went
// perfectly, and the journal says
//
//	samba[…]: /usr/sbin/winbindd: Failed to exec child - No such file or directory
//	samba[…]: winbindd daemon died with exit status 255
//	samba[…]: samba_terminate: …: winbindd child process exited
func winbinddMissing() bool {
	for _, path := range winbinddPaths {
		if info, err := statFile(path); err == nil && !info.IsDir() {
			return false
		}
	}
	return true
}

// The units a Debian or Ubuntu host runs the distribution's own file server
// under, in the order systemd lists them.
var fileServerUnits = []string{"smbd.service", "nmbd.service"}

// unitFileDirs is where a packaged unit file lives, and unitWantsGlob is how
// systemd records that one is enabled: a symlink in a .wants directory under
// /etc/systemd/system, which is what `systemctl enable` writes and what
// `systemctl is-enabled` reads back. Looking for the link is the same fact
// without a process.
var unitFileDirs = []string{"/usr/lib/systemd/system", "/lib/systemd/system"}

const unitWantsGlob = "/etc/systemd/system/*.wants/"

// EnabledFileServerUnits returns the file server units that are installed on
// this host and enabled, in order.
//
// Both halves are required and both are facts on disk. "Installed" is the unit
// file; "enabled" is the symlink systemd would follow at boot. Fedora and Arch
// ship no smbd.service or nmbd.service at all — their file server is
// smb.service and nmb.service, and neither is enabled by installing samba — so
// this answers empty there, which is why the step below exists only where it is
// needed.
func EnabledFileServerUnits() []string {
	var enabled []string
	for _, unit := range fileServerUnits {
		if !unitFileInstalled(unit) || !unitEnabled(unit) {
			continue
		}
		enabled = append(enabled, unit)
	}
	return enabled
}

// unitFileInstalled reports that a package put this unit on disk.
func unitFileInstalled(unit string) bool {
	for _, dir := range unitFileDirs {
		if info, err := statFile(dir + "/" + unit); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// unitEnabled reports that systemd would start this unit at boot, by the link
// that says so. Any target's .wants directory counts: what matters is that
// something wants the unit, not which target does.
func unitEnabled(unit string) bool {
	matches, err := globPaths(unitWantsGlob + unit)
	return err == nil && len(matches) > 0
}

// FileServerDisable is the previewed step that takes the distribution's own file
// server out of the controller's way, offered only on a host that has one
// enabled.
//
// The chain, verified on Ubuntu 24.04.5 with samba 4.19.5: `samba` in AD DC mode
// forks its own smbd, and Debian and Ubuntu enable smbd.service and nmbd.service
// when the samba package is installed. Those hold 139 and 445, the AD DC's smbd
// cannot bind them, and the unit exits with a journal that says nothing about a
// port:
//
//	samba[…]: samba_terminate: …: smbd child process exited
//
// Stopping and disabling them is what makes `systemctl enable --now
// samba-ad-dc.service` succeed, which is why this step comes before it.
//
// It is one step and not two, and the units it names are only the ones this host
// actually has enabled. The reason is that they are one fact — this host serves
// files, and the controller cannot serve a domain while it does — and clearing
// half of it changes nothing a reader could observe: a confirm that stops smbd
// and leaves nmbd enabled leaves the controller exactly as unable to start, so
// it would be a dialog that cannot succeed on its own. Two previewed commands
// are worth two confirms when each achieves something; these do not.
func FileServerDisable() (runner.Command, bool) {
	units := EnabledFileServerUnits()
	if len(units) == 0 {
		return runner.Command{}, false
	}
	return runner.Command{
		Argv:        append([]string{"systemctl", "disable", "--now"}, units...),
		Description: "Stop and disable " + strings.Join(units, " and "),
		// Stopping a file server disconnects whoever is using it. Nothing here
		// is irreversible — `systemctl enable --now` puts both back — but it is
		// a service going away, and this tool asks before one does.
		Destructive: true,
	}, true
}

// FileServerDisableBody is what the confirm dialog says about that step. Like
// the Kerberos one it lives here because the reason is a fact about this
// backend, not about the screen showing it.
const FileServerDisableBody = "Debian and Ubuntu enable the standalone file " +
	"server when the samba package is installed, and it holds ports 139 and " +
	"445. A domain controller runs its own smbd on those ports, so while " +
	"these units are up the AD DC unit starts and exits again, with nothing " +
	"in the journal but \"smbd child process exited\". This stops them and " +
	"takes them out of the boot, before the controller is started — so on a " +
	"host that is also serving shares today, those shares stop being served: " +
	"an AD DC serves the domain's own sysvol and netlogon instead. On a host " +
	"that is only going to be a domain controller that is what you want."

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
