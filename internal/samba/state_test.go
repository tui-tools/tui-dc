package samba

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-kit/runner"
)

// brokenOutput is what samba-tool 4.25.0 printed on Arch with
// python-cryptography absent (tui-tools/tui-dc#23), captured on an Arch
// container: the program is there, Python starts, and an import fails before
// samba-tool answers anything, --version included. The first line is
// coloured, as samba-tool colours it.
const brokenOutput = "\x1b[31mERROR\x1b[0m(<class 'ModuleNotFoundError'>): " +
	"uncaught exception - No module named 'cryptography'\n" +
	`  File "/usr/lib/python3.14/site-packages/samba/netcmd/main.py", line 89, in samba_tool
    ret = cmd._run(*argv)
  File "/usr/lib/python3.14/site-packages/samba/netcmd/__init__.py", line 475, in _run
    cmd = self.subcommands[cmd_name]
          ~~~~~~~~~~~~~~~~^^^^^^^^^^
  File "/usr/lib/python3.14/site-packages/samba/netcmd/computer_generate_csr.py", line 21, in <module>
    from cryptography.hazmat.primitives import serialization
`

// bareTraceback is the same failure as plain Python reports it, for a
// samba-tool that dies before its own handler is installed.
const bareTraceback = `Traceback (most recent call last):
  File "/usr/bin/samba-tool", line 33, in <module>
    from samba.netcmd.main import samba_tool
ModuleNotFoundError: No module named 'cryptography'
`

// scriptedReader runs a stand-in samba-tool through the kit's real runner, so
// the error loadDomain sees is the one a real host produces (the runner's own
// wording around the program's output), not one a test made up. With no
// script the binary does not exist at all.
func scriptedReader(t *testing.T, script string) readFunc {
	t.Helper()
	dir := t.TempDir()
	// PATH is the one place the runner looks besides the fallbacks, so the
	// stand-in's directory comes first on it: a samba-tool installed on the
	// machine running the test must not answer for the stand-in. With no
	// stand-in, PATH is that directory alone, so no samba-tool is found.
	var paths []string
	if script != "" {
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		bin := filepath.Join(dir, directory.Bin)
		// The stand-in has to be executable to stand in for a program.
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { //nolint:gosec // an executable test fixture in t.TempDir()
			t.Fatal(err)
		}
		paths = []string{bin}
	} else {
		t.Setenv("PATH", dir)
		paths = []string{filepath.Join(dir, "absent", directory.Bin)}
	}
	unprivileged := false
	r, err := runner.New(runner.Options{
		Bin:             directory.Bin,
		SearchPaths:     paths,
		PrivilegedReads: &unprivileged,
	})
	if err != nil {
		// The absent case: Real.read answers exactly this way when NewReal
		// found no binary (see Real.unavailable).
		return func(context.Context, ...string) (string, error) { return "", err }
	}
	return func(ctx context.Context, args ...string) (string, error) {
		return r.Read(ctx, append([]string{directory.Bin}, args...)...)
	}
}

// TestSambaToolStates pins the three things a machine's samba-tool can be,
// and that each is said one way: absent, present and unable to run, present
// and running. The middle one used to read as "not installed" (#23).
func TestSambaToolStates(t *testing.T) {
	// printf rather than a heredoc: the escape in the first line has to
	// reach the runner as the byte samba-tool writes.
	script := func(output string) string {
		return "#!/bin/sh\nprintf '%s' '" +
			strings.ReplaceAll(output, "'", `'\''`) + "' >&2\nexit 1\n"
	}
	cases := []struct {
		name          string
		script        string
		wantInstalled bool
		wantRuns      bool
		wantCannotRun string
		wantState     string
	}{
		{
			name:          "absent",
			wantInstalled: false,
			wantState:     "not installed",
		},
		{
			name:          "installed, cannot run (samba-tool's own report)",
			script:        script(brokenOutput),
			wantInstalled: true,
			wantCannotRun: "ModuleNotFoundError: No module named 'cryptography'",
			wantState: "installed but cannot run: " +
				"ModuleNotFoundError: No module named 'cryptography'",
		},
		{
			name:          "installed, cannot run (bare traceback)",
			script:        script(bareTraceback),
			wantInstalled: true,
			wantCannotRun: "ModuleNotFoundError: No module named 'cryptography'",
			wantState: "installed but cannot run: " +
				"ModuleNotFoundError: No module named 'cryptography'",
		},
		{
			name:          "installed, runs",
			script:        "#!/bin/sh\necho 4.22.4\n[ \"$1\" = --version ] && exit 0\nexit 1\n",
			wantInstalled: true,
			wantRuns:      true,
			wantState:     "installed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, _ := loadDomain(context.Background(),
				scriptedReader(t, tc.script), DefaultServer)
			if model.Installed != tc.wantInstalled {
				t.Errorf("Installed = %v, want %v", model.Installed, tc.wantInstalled)
			}
			if model.Runs() != tc.wantRuns {
				t.Errorf("Runs() = %v, want %v", model.Runs(), tc.wantRuns)
			}
			if model.CannotRun != tc.wantCannotRun {
				t.Errorf("CannotRun = %q, want %q", model.CannotRun, tc.wantCannotRun)
			}
			state := directory.SambaToolState(model.Installed, model.CannotRun)
			if state != tc.wantState {
				t.Errorf("state = %q, want %q", state, tc.wantState)
			}
			if tc.wantCannotRun != "" {
				// The status line says the same sentence as everything else.
				if want := "samba-tool is " + tc.wantState; model.Detail != want {
					t.Errorf("Detail = %q, want %q", model.Detail, want)
				}
				if model.Version != "" {
					t.Errorf("a samba-tool that did not run has version %q",
						model.Version)
				}
			}
			if !tc.wantInstalled && !strings.Contains(model.Detail, "not found") {
				t.Errorf("Detail = %q, want the runner's not-found message",
					model.Detail)
			}
		})
	}
}

// TestCannotRun covers the reason's shape: empty for no error and for an
// absent binary, one short clean line otherwise.
func TestCannotRun(t *testing.T) {
	long := strings.Repeat("x", 300)
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "no error", err: nil, want: ""},
		{
			name: "absent",
			err:  fmt.Errorf("%w: the samba-tool command was not found", ErrNotAvailable),
			want: "",
		},
		{
			name: "command line dropped",
			err:  errors.New("`sudo -n /usr/bin/samba-tool --version` failed: ImportError: libldb.so.2"),
			want: "ImportError: libldb.so.2",
		},
		{
			name: "samba-tool's uncaught exception condensed",
			err: errors.New("`samba-tool --version` failed: " +
				"ERROR(<class 'ImportError'>): uncaught exception - libldb.so.2: cannot open"),
			want: "ImportError: libldb.so.2: cannot open",
		},
		{
			name: "escalation refused",
			err:  errors.New("sudo needs a password: run `sudo -v` in another terminal, then retry"),
			want: "sudo needs a password: run `sudo -v` in another terminal, then retry",
		},
		{
			name: "terminal control removed",
			err:  errors.New("`samba-tool --version` failed: \x1b[31mfatal\x1b[0m: broken"),
			want: "fatal: broken",
		},
		{
			name: "capped",
			err:  errors.New("`samba-tool --version` failed: " + long),
			want: strings.Repeat("x", maxCannotRun-3) + "...",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CannotRun(tc.err); got != tc.want {
				t.Errorf("CannotRun = %q, want %q", got, tc.want)
			}
		})
	}
}
