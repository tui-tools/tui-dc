package tuidc

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/manifest"
)

// The manifest is code. Its `versionRegex` is the only thing standing between
// `samba-tool --version` and a version number the header states as fact, and it
// runs through the kit rather than through anything in this repository — so
// nothing here had ever exercised it, and it was wrong: unanchored, it matched
// `3.14` out of `/usr/lib/python3.14/…` in the traceback samba-tool prints on
// an Arch host whose `samba` package brought no `python-cryptography`, and the
// header read `samba 3.14 (below minimum 4.13)` on a machine running 4.24.7.
//
// These tests drive the shipped manifest through compat.ProbeWith with the
// process replaced, which is the same path the binary takes. The kit applies
// the pattern to the whole output with FindStringSubmatch, which is why the
// pattern is `(?m)`-anchored per line rather than at the start of the output:
// samba-tool has printed a usage complaint before the version before now, and
// testdata/version.txt is a capture of it.

// sambaBackend is the backend block the binary probes with.
func sambaBackend(t *testing.T) compat.Backend {
	t.Helper()
	m, err := manifest.Load(ManifestJSON)
	if err != nil {
		t.Fatalf("loading tool.json: %v", err)
	}
	backend, ok := m.Backend("samba")
	if !ok {
		t.Fatal("tool.json declares no samba backend")
	}
	return backend
}

// probe runs the manifest's probe against one canned output, with the exit
// status samba-tool left behind it.
func probe(t *testing.T, backend compat.Backend, out string, err error) compat.Result {
	t.Helper()
	return compat.ProbeWith(context.Background(), backend,
		func(context.Context, []string) (string, error) { return out, err })
}

// TestVersionRegexAcceptsRealOutput holds the three strings `samba-tool
// --version` really printed, one per distribution, in the three-guest matrix
// run the bug came out of: Fedora 44, Ubuntu 24.04 and Omarchy Server 4.0.1.
// A pattern strict enough to reject a traceback is only worth having if it
// still reads these.
func TestVersionRegexAcceptsRealOutput(t *testing.T) {
	backend := sambaBackend(t)
	for _, tc := range []struct{ out, want, where string }{
		{"4.24.6\n", "4.24.6", "Fedora 44"},
		{"4.19.5-Ubuntu\n", "4.19.5", "Ubuntu 24.04"},
		{"4.24.7\n", "4.24.7", "Omarchy Server 4.0.1"},
	} {
		result := probe(t, backend, tc.out, nil)
		if result.Version != tc.want {
			t.Errorf("%s: version = %q, want %q (detail: %s)",
				tc.where, result.Version, tc.want, result.Detail)
		}
		if result.Status == compat.StatusBelowMinimum {
			t.Errorf("%s: %q read as below the minimum %s",
				tc.where, result.Version, backend.Minimum)
		}
	}
}

// TestVersionRegexAcceptsUsageBanner is the captured output in
// internal/samba/testdata: some releases print a complaint about the missing
// subcommand before the version, so the version is not on the first line. The
// per-line anchor has to allow that, which is why it is not anchored to the
// start of the output.
func TestVersionRegexAcceptsUsageBanner(t *testing.T) {
	// #nosec G304 -- this repository's own fixture, at a fixed path.
	data, err := os.ReadFile("internal/samba/testdata/version.txt")
	if err != nil {
		t.Fatalf("reading the version fixture: %v", err)
	}
	result := probe(t, sambaBackend(t), string(data), nil)
	if result.Version != "4.22.10" {
		t.Errorf("version = %q, want 4.22.10 (detail: %s)",
			result.Version, result.Detail)
	}
}

// TestVersionRegexRejectsTraceback is the bug. On Arch the `samba` package does
// not depend on `python-cryptography`, so samba-tool can be installed and still
// fail to start: exit 1, a traceback, no version. The kit parses the output of a
// non-zero exit on purpose — some tools print a version and exit non-zero — so
// the pattern is what has to refuse this, and no version at all is the honest
// answer. Stating a version the machine does not have makes every compatibility
// sentence after it false.
func TestVersionRegexRejectsTraceback(t *testing.T) {
	traceback := "Traceback (most recent call last):\n" +
		"  File \"/usr/bin/samba-tool\", line 33, in <module>\n" +
		"    from samba.netcmd.main import cmd_sambatool\n" +
		"  File \"/usr/lib/python3.14/site-packages/samba/netcmd/__init__.py\"," +
		" line 29, in <module>\n" +
		"    from cryptography.hazmat.primitives import hashes\n" +
		"ModuleNotFoundError: No module named 'cryptography'\n"

	result := probe(t, sambaBackend(t), traceback,
		errors.New("exit status 1"))
	if result.Version != "" {
		t.Fatalf("a version was read out of a traceback: %q", result.Version)
	}
	if result.Status != compat.StatusUnknown {
		t.Errorf("status = %v, want unknown", result.Status)
	}
	// And the reason is the one the tool can print: the header says the version
	// is unknown, not that samba is older than it is.
	if !strings.Contains(result.Detail, "could not read a version") {
		t.Errorf("detail = %q", result.Detail)
	}
}
