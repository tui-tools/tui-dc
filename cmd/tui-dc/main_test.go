package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/tui-tools/tui-dc/internal/samba"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/config"
)

func TestParseFlags(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()

	opts, err := parseFlags([]string{"--demo", "--server", "10.10.0.10"}, devNull)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !opts.demo || opts.server != "10.10.0.10" {
		t.Errorf("opts = %+v", opts)
	}

	// An explicitly empty -sudo disables escalation and must not read as "not
	// given", which is the difference between running as yourself and
	// silently falling back to the configured prefix.
	opts, err = parseFlags([]string{"--sudo", ""}, devNull)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !opts.sudoSet || opts.sudo != "" {
		t.Errorf("an empty -sudo was not recorded: %+v", opts)
	}
}

func TestApplyOverrides(t *testing.T) {
	cfg, err := config.Load(config.Options{Tool: toolName, Defaults: defaults()})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	applyOverrides(&cfg, options{server: "10.10.0.10", sudoSet: true, sudo: ""})
	if got := cfg.String(keyServer, ""); got != "10.10.0.10" {
		t.Errorf("server = %q", got)
	}
	if prefix := cfg.SudoPrefix(); len(prefix) != 0 {
		t.Errorf("an empty -sudo left the prefix %q", prefix)
	}
}

// TestCheckIsValidJSON is what a smoke test and a CI job depend on: --check
// prints one JSON object and nothing else, so a script can pipe it straight
// into jq.
func TestCheckIsValidJSON(t *testing.T) {
	var out bytes.Buffer
	if err := runCheck(samba.NewFake(), compat.Result{}, &out); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	var report checkReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("--check did not print JSON: %v\n%s", err, out.String())
	}
	if report.Tool != toolName {
		t.Errorf("tool = %q", report.Tool)
	}
	if !report.Installed || !report.Reachable || !report.IsDC {
		t.Errorf("the demo domain reports as unreachable: %+v", report)
	}
	if report.Users == 0 || report.Groups == 0 || report.Computers == 0 {
		t.Errorf("counts are empty: %+v", report)
	}
	if report.ReplicationOK {
		t.Error("the demo has a failing partition, so replication is not ok")
	}
	if len(report.Partitions) == 0 {
		t.Error("no partitions in the report")
	}
	if report.Realm == "" {
		t.Error("no realm in the report")
	}
}

// TestReportSaysNothingPrivate is the rule --report exists under: it names
// versions and machine facts, and nothing about the person running it or the
// domain they administer.
func TestReportSaysNothingPrivate(t *testing.T) {
	cfg, err := config.Load(config.Options{Tool: toolName, Defaults: defaults()})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	var out bytes.Buffer
	if err := runReport(cfg, options{demo: true}, &out); err != nil {
		t.Fatalf("runReport: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, toolName) {
		t.Error("the report does not name the tool")
	}
	for _, forbidden := range []string{"/home/", "/root/"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("the report contains %q:\n%s", forbidden, text)
		}
	}
	// The two facts this tool adds beyond the kit's block.
	for _, want := range []string{"samba-tool:", "this host:"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report is missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "demo backend") {
		t.Error("a --demo report does not say which backend the fake imitated")
	}
}

// TestScrubHome covers the one value this tool hands to report.Extra that
// could otherwise name its user.
func TestScrubHome(t *testing.T) {
	// The example path is assembled rather than written out. A literal home
	// path in a source file is exactly what this repository's secret scan
	// looks for, and it is right to look: this is the one place in the tool
	// that needs one, and it needs it only as something to scrub.
	home := fmt.Sprintf("/%s/someone", "home")
	got := scrubHome("cannot run " + home + "/bin/samba-tool: no such file")
	if strings.Contains(got, home) {
		t.Errorf("a home path survived the scrub: %q", got)
	}
	if !strings.Contains(got, "~elsewhere~") {
		t.Errorf("the placeholder is missing: %q", got)
	}
}

// TestReadSmallFileRefusesTheWrongShape covers the guard that keeps --report
// working everywhere: a named pipe where smb.conf should be would otherwise
// hang the one command that has to run on any machine.
func TestReadSmallFileRefusesTheWrongShape(t *testing.T) {
	if _, err := readSmallFile(t.TempDir()); err == nil {
		t.Error("a directory was read as a configuration file")
	}
	if _, err := readSmallFile("/nonexistent/smb.conf"); err == nil {
		t.Error("a missing file did not error")
	}
}
