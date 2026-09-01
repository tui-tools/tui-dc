package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-dc/internal/samba"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/theme"
)

// newTestApp builds the app around the fake backend and completes its first
// load, so a test starts where a user does: on a drawn screen.
func newTestApp(t *testing.T) (*app, *samba.Fake) {
	t.Helper()
	fake := samba.NewFake()
	a := newApp(fake, theme.New(), compat.Result{})
	a.width, a.height = 120, 40

	cmd := a.Init()
	if cmd == nil {
		t.Fatal("Init did not start a load")
	}
	a.Update(cmd())
	if !a.model.Installed {
		t.Fatal("the demo domain did not load")
	}
	return a, fake
}

// press sends one key and drains whatever command it returned, the way the
// Bubble Tea runtime would.
func press(t *testing.T, a *app, key string) {
	t.Helper()
	var msg tea.Msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	}
	_, cmd := a.Update(msg)
	for cmd != nil {
		next := cmd()
		if next == nil {
			return
		}
		_, cmd = a.Update(next)
	}
}

// TestDemoLoadsTheWholeDomain is the assertion --demo exists for: every screen
// has something on it, so the demo exercises every parser in the backend.
func TestDemoLoadsTheWholeDomain(t *testing.T) {
	a, _ := newTestApp(t)
	counts := a.model.Count()
	if counts.Users == 0 || counts.Groups == 0 || counts.Computers == 0 {
		t.Errorf("the demo domain is missing objects: %+v", counts)
	}
	if !a.model.Zone.Read || counts.Records == 0 {
		t.Error("the demo zone was not read")
	}
	if !a.model.Repl.Read || counts.Partitions == 0 {
		t.Error("the demo replication was not read")
	}
	if counts.Failing == 0 {
		t.Error("the demo has no failing partition, so the failure path is never drawn")
	}
	if a.model.Domain.Realm == "" || !a.model.Domain.IsDC() {
		t.Errorf("the demo domain is not a domain: %+v", a.model.Domain)
	}
}

// TestEveryScreenDraws checks the four bands render at both a wide and a very
// narrow terminal, on every tab. A column layout that overflows is the family's
// most common regression and it is invisible until somebody resizes.
func TestEveryScreenDraws(t *testing.T) {
	a, _ := newTestApp(t)
	for _, width := range []int{40, 80, 120} {
		a.width, a.height = width, 24
		a.applyFilter()
		for screen := directory.Screen(0); screen < directory.ScreenCount; screen++ {
			a.screen = screen
			a.clampCursor()
			view := a.View()
			if strings.TrimSpace(view) == "" {
				t.Fatalf("the %s screen drew nothing at %d columns",
					screen.Title(), width)
			}
			for i, line := range strings.Split(view, "\n") {
				if got := len([]rune(stripANSI(line))); got > width {
					t.Errorf("%s at %d columns: line %d is %d wide",
						screen.Title(), width, i, got)
				}
			}
		}
	}
}

// stripANSI removes the escape sequences lipgloss writes, so a width check
// measures characters rather than colour codes.
func stripANSI(s string) string {
	var out strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEscape = true
		case inEscape && (r == 'm' || r == 'K' || r == 'H'):
			inEscape = false
		case !inEscape:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// TestDisableRunsExactlyThePreviewedCommand is the assertion the whole family
// rests on: one key press, one confirm, and the command that ran is the one the
// dialog showed — the same value, not a rebuilt one.
func TestDisableRunsExactlyThePreviewedCommand(t *testing.T) {
	a, fake := newTestApp(t)
	a.screen = directory.ScreenUsers
	a.applyFilter()
	selectRow(t, a, "alice")

	press(t, a, "s")
	if a.mode != modeConfirm {
		t.Fatalf("pressing s did not open the confirm dialog (mode %d)", a.mode)
	}
	preview := a.confirm.Command
	if preview != "samba-tool user disable alice" {
		t.Fatalf("the dialog shows %q", preview)
	}
	if !a.confirm.Danger {
		t.Error("suspending an account is destructive and should be painted so")
	}
	if len(fake.Commands()) != 0 {
		t.Fatal("something ran before the dialog was answered")
	}

	press(t, a, "y")
	ran := fake.Commands()
	if len(ran) != 1 {
		t.Fatalf("%d commands ran, want exactly 1: %+v", len(ran), ran)
	}
	if got := ran[0].String(); got != preview {
		t.Errorf("ran %q, previewed %q", got, preview)
	}
}

// TestCancellingRunsNothing is the other half of the same contract.
func TestCancellingRunsNothing(t *testing.T) {
	a, fake := newTestApp(t)
	a.screen = directory.ScreenUsers
	a.applyFilter()
	selectRow(t, a, "alice")

	press(t, a, "d")
	if a.mode != modeConfirm {
		t.Fatal("pressing d did not open the confirm dialog")
	}
	press(t, a, "n")
	if len(fake.Commands()) != 0 {
		t.Errorf("a cancelled dialog ran %+v", fake.Commands())
	}
	if a.mode != modeBrowse {
		t.Error("cancelling did not return to the list")
	}
}

// TestPromptedActionUsesWhatWasTyped covers the actions that need a value the
// selected row cannot supply.
func TestPromptedActionUsesWhatWasTyped(t *testing.T) {
	a, fake := newTestApp(t)
	a.screen = directory.ScreenGroups
	a.applyFilter()
	selectRow(t, a, "Helpdesk")

	press(t, a, "a")
	if a.mode != modeInput {
		t.Fatalf("pressing a did not open a prompt (mode %d)", a.mode)
	}
	typeInto(t, a, "bob")
	press(t, a, "enter")
	if a.mode != modeConfirm {
		t.Fatalf("accepting the prompt did not open the confirm dialog (mode %d)", a.mode)
	}
	if got := a.confirm.Command; got != "samba-tool group addmembers Helpdesk bob" {
		t.Fatalf("the dialog shows %q", got)
	}
	press(t, a, "y")
	if ran := fake.Commands(); len(ran) != 1 ||
		ran[0].String() != "samba-tool group addmembers Helpdesk bob" {
		t.Errorf("ran %+v", ran)
	}
}

// TestReadOnlyScreensRunNothing states the boundary from the UI's side: on
// the computers and replication screens, no key builds a command. The domain
// screen is no longer in this list — it carries the policy edit and, on a
// machine with no domain, the provision wizard — and its own guardrails are
// tested in wizard_test.go.
func TestReadOnlyScreensRunNothing(t *testing.T) {
	for _, screen := range []directory.Screen{
		directory.ScreenComputers, directory.ScreenRepl,
	} {
		a, fake := newTestApp(t)
		a.screen = screen
		a.applyFilter()
		for _, key := range []string{"n", "d", "e", "s", "p", "x", "a", "m"} {
			press(t, a, key)
			if a.mode == modeConfirm || a.mode == modeInput {
				t.Fatalf("%q opened a dialog on the read-only %s screen",
					key, screen.Title())
			}
		}
		if ran := fake.Commands(); len(ran) != 0 {
			t.Errorf("the %s screen ran %+v", screen.Title(), ran)
		}
	}
}

// TestDetailReadsOnDemand covers the lazy read: a row's detail is not there
// until it is opened, and then it is.
func TestDetailReadsOnDemand(t *testing.T) {
	a, _ := newTestApp(t)
	a.screen = directory.ScreenUsers
	a.applyFilter()
	selectRow(t, a, "bob")

	if _, read := a.userDetail["bob"]; read {
		t.Fatal("an account was read before it was opened")
	}
	press(t, a, "enter")
	user, read := a.userDetail["bob"]
	if !read {
		t.Fatal("opening an account did not read it")
	}
	if !user.Disabled {
		t.Error("bob is disabled in the demo domain and the detail says otherwise")
	}
	if a.mode != modeDetail {
		t.Error("enter did not open the detail screen")
	}
}

// TestChangeRefreshesWhatIsOnScreen guards the bug a detail cache invites: an
// account that was just enabled still showing as disabled. The whole path runs
// — key, dialog, command, reload, re-read — and the row has to end up saying
// what the domain now says.
func TestChangeRefreshesWhatIsOnScreen(t *testing.T) {
	a, _ := newTestApp(t)
	a.screen = directory.ScreenUsers
	a.applyFilter()
	selectRow(t, a, "bob")

	press(t, a, "enter")
	press(t, a, "esc")
	if user, read := a.userDetail["bob"]; !read || !user.Disabled {
		t.Fatalf("bob starts disabled in the demo domain: read=%v %+v", read, user)
	}

	selectRow(t, a, "bob")
	press(t, a, "e")
	if a.mode != modeConfirm {
		t.Fatalf("pressing e did not open the confirm dialog (mode %d)", a.mode)
	}
	press(t, a, "y")

	// The reload after the change dropped the stale detail; open the row again
	// and it must come back enabled.
	selectRow(t, a, "bob")
	press(t, a, "enter")
	if user, read := a.userDetail["bob"]; !read || user.Disabled {
		t.Errorf("bob is still disabled after being enabled: read=%v %+v", read, user)
	}
}

// TestFilterAppliesToEveryScreen checks that the filter narrows what is on
// screen rather than what is in the model.
func TestFilterAppliesToEveryScreen(t *testing.T) {
	a, _ := newTestApp(t)
	a.screen = directory.ScreenUsers
	a.filter = "alice"
	a.applyFilter()
	if len(a.userRows) != 1 || a.userRows[0].Name != "alice" {
		t.Errorf("filtered accounts = %+v", a.userRows)
	}
	if len(a.model.Users) < 2 {
		t.Error("the filter changed the model instead of the view")
	}
	a.filter = "zzzz-nothing"
	a.applyFilter()
	if len(a.userRows) != 0 {
		t.Errorf("a filter matching nothing left %d rows", len(a.userRows))
	}
	if !strings.Contains(a.View(), "nothing matches") {
		t.Error("an empty filtered screen does not say why it is empty")
	}
}

// selectRow moves the cursor onto a named row on the current screen.
func selectRow(t *testing.T, a *app, name string) {
	t.Helper()
	for i := 0; i < a.rowCount(); i++ {
		a.cursor[a.screen] = i
		if got, ok := a.selectedName(); ok && got == name {
			return
		}
	}
	t.Fatalf("no row named %q on the %s screen", name, a.screen.Title())
}

// typeInto sends a string to an open prompt.
func typeInto(t *testing.T, a *app, text string) {
	t.Helper()
	for _, r := range text {
		a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}
