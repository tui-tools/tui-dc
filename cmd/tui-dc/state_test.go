package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-dc/internal/samba"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/theme"
)

// TestSambaToolSaidOneWay is tui-tools/tui-dc#23: one machine, one samba-tool,
// and every place that describes it (the empty screen, the subtitle, the
// domain screen's fact row, the status line and --report) agrees on what it
// is. A samba-tool that is installed and cannot run is neither "not installed"
// nor "version unknown".
func TestSambaToolSaidOneWay(t *testing.T) {
	const reason = "ModuleNotFoundError: No module named 'cryptography'"
	cases := []struct {
		name     string
		model    directory.Model
		probeErr error
		version  string
		// What each surface must say. The report line is the --report value
		// for the same machine, probed unprivileged.
		empty, subtitle, fact, report string
		offered                       bool
	}{
		{
			name:     "not installed",
			model:    directory.Model{Detail: "command not available: the samba-tool command was not found"},
			probeErr: fmt.Errorf("%w: the samba-tool command was not found", samba.ErrNotAvailable),
			empty:    "samba-tool is not installed here",
			subtitle: "no samba-tool",
			fact:     "not installed",
			report:   "not installed",
		},
		{
			name: "installed, cannot run",
			model: directory.Model{
				Installed: true,
				CannotRun: reason,
				Detail:    "samba-tool is installed but cannot run: " + reason,
			},
			probeErr: errors.New("`/usr/bin/samba-tool --version` failed: " + reason),
			empty:    "samba-tool is installed but cannot run: " + reason,
			subtitle: "samba-tool cannot run",
			fact:     "installed but cannot run: " + reason,
			report:   "installed but cannot run: " + reason,
		},
		{
			name:     "installed, runs",
			model:    directory.Model{Installed: true, Version: "4.22.4"},
			version:  "4.22.4",
			subtitle: "domain unknown",
			fact:     "4.22.4",
			report:   "installed",
			offered:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(samba.NewFake(), theme.New(), compat.Result{})
			a.width, a.height = 120, 40
			a.Update(loadedMsg{model: tc.model})

			if tc.empty != "" {
				if got := a.emptyMessage(); got != tc.empty {
					t.Errorf("empty screen = %q, want %q", got, tc.empty)
				}
			}
			if got := a.subtitle(); !strings.HasPrefix(got, tc.subtitle) {
				t.Errorf("subtitle = %q, want it to start with %q", got, tc.subtitle)
			}
			if got := sambaToolFact(a.model); got != tc.fact {
				t.Errorf("fact row = %q, want %q", got, tc.fact)
			}
			if tc.model.Detail != "" && a.status != tc.model.Detail {
				t.Errorf("status line = %q, want %q", a.status, tc.model.Detail)
			}
			if got := a.provisionOffered(); got != tc.offered {
				t.Errorf("provision offered = %v, want %v", got, tc.offered)
			}

			defer func(saved func() bool) { sambaAvailable = saved }(sambaAvailable)
			sambaAvailable = func() bool { return tc.model.Installed }
			if got := sambaToolLine(tc.version, tc.probeErr); got != tc.report {
				t.Errorf("--report samba-tool = %q, want %q", got, tc.report)
			}
		})
	}
}
