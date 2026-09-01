package main

import (
	"strings"
	"testing"

	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-dc/internal/samba"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/theme"
)

// newFreshTestApp builds the app around a machine with samba-tool and no
// domain — what --demo-fresh shows.
func newFreshTestApp(t *testing.T) (*app, *samba.Fake) {
	t.Helper()
	fake := samba.NewFakeFresh()
	a := newApp(fake, theme.New(), compat.Result{})
	a.width, a.height = 120, 40
	cmd := a.Init()
	if cmd == nil {
		t.Fatal("Init did not start a load")
	}
	a.Update(cmd())
	if !a.model.Installed || a.model.Domain.IsDC() {
		t.Fatalf("the fresh machine did not load as one: %+v", a.model.Domain)
	}
	return a, fake
}

// TestProvisionWizardEndToEnd walks the whole path a first boot takes: the
// offer on the empty domain screen, the wizard's five steps, the typed-realm
// gate, the previewed command, the one-time password on the result screen,
// and the previewed systemctl step after it.
func TestProvisionWizardEndToEnd(t *testing.T) {
	a, fake := newFreshTestApp(t)

	// The offer is on the screen, not only in the help.
	if !strings.Contains(a.View(), "press P to provision") {
		t.Error("the domain screen does not offer the wizard")
	}

	press(t, a, "P")
	if a.mode != modeWizard {
		t.Fatalf("P did not open the wizard (mode %d)", a.mode)
	}

	// Step 1: an invalid realm is refused with the wizard still open.
	typeInto(t, a, "corp")
	press(t, a, "enter")
	if a.mode != modeWizard || a.wizard.step != wizRealm {
		t.Fatal("a realm without a dot advanced the wizard")
	}
	typeInto(t, a, ".internal") // completes corp.internal
	press(t, a, "enter")
	if a.wizard.step != wizNetBIOS {
		t.Fatalf("the wizard did not reach the NetBIOS step (step %d)", a.wizard.step)
	}
	// Step 2: the suggestion (CORP) is prefilled; accept it.
	if a.wizard.input.Value() != "CORP" {
		t.Errorf("the NetBIOS suggestion is %q, want CORP", a.wizard.input.Value())
	}
	press(t, a, "enter")
	// Step 3: the DNS backend picker, defaulting to SAMBA_INTERNAL.
	if a.wizard.step != wizBackend {
		t.Fatalf("the wizard did not reach the backend step (step %d)", a.wizard.step)
	}
	press(t, a, "enter")
	// Step 4: the forwarder.
	typeInto(t, a, "10.0.0.1")
	press(t, a, "enter")
	// Step 5: the typed-realm gate refuses anything but the realm.
	if a.wizard.step != wizTyped {
		t.Fatalf("the wizard did not reach the deliberate step (step %d)", a.wizard.step)
	}
	typeInto(t, a, "yes")
	press(t, a, "enter")
	if a.mode != modeWizard {
		t.Fatal("typing something other than the realm got past the gate")
	}
	typeInto(t, a, "corp.internal")
	press(t, a, "enter")

	if a.mode != modeConfirm {
		t.Fatalf("the wizard did not end in the confirm dialog (mode %d)", a.mode)
	}
	want := "samba-tool domain provision --realm=CORP.INTERNAL --domain=CORP " +
		"--server-role=dc --dns-backend=SAMBA_INTERNAL --dns-forwarder=10.0.0.1"
	if a.confirm.Command != want {
		t.Fatalf("the dialog shows %q\n want %q", a.confirm.Command, want)
	}
	if !a.confirm.Danger {
		t.Error("a provision must be painted as destructive")
	}
	if strings.Contains(a.confirm.Command, "adminpass") {
		t.Error("an adminpass reached the previewed command line")
	}
	if len(fake.Commands()) != 0 {
		t.Fatal("something ran before the dialog was answered")
	}

	press(t, a, "y")
	ran := fake.Commands()
	if len(ran) != 1 || ran[0].String() != want {
		t.Fatalf("ran %+v, want exactly the previewed command", ran)
	}

	// The result screen shows the password samba-tool printed, once.
	if a.mode != modeNotice {
		t.Fatalf("the provision did not end on the result screen (mode %d)", a.mode)
	}
	view := a.View()
	if !strings.Contains(view, "eiXi4nu3Ooquiet~aiZ0") {
		t.Error("the result screen does not show the one-time Admin password")
	}
	if !strings.Contains(view, "krb5.conf") {
		t.Error("the result screen does not carry the krb5.conf note")
	}
	if !a.model.Domain.IsDC() || a.model.Domain.Realm != "corp.internal" {
		t.Errorf("the reload under the notice did not see the domain: %+v",
			a.model.Domain)
	}

	// Enter offers the previewed systemctl step; confirming runs exactly it.
	press(t, a, "enter")
	if a.mode != modeConfirm {
		t.Fatalf("the notice did not offer the service step (mode %d)", a.mode)
	}
	if a.confirm.Command != "systemctl enable --now samba-ad-dc.service" {
		t.Fatalf("the service dialog shows %q", a.confirm.Command)
	}
	press(t, a, "y")
	ran = fake.Commands()
	last := ran[len(ran)-1]
	if last.String() != "systemctl enable --now samba-ad-dc.service" {
		t.Errorf("the service step ran %q", last.String())
	}
}

// TestWizardRefusedWhereADomainExists is the guard from the UI's side and the
// backend's: on a host that already serves a domain, P opens nothing and the
// builder refuses.
func TestWizardRefusedWhereADomainExists(t *testing.T) {
	a, fake := newTestApp(t)
	press(t, a, "P")
	if a.mode != modeBrowse {
		t.Fatalf("P opened something on a provisioned host (mode %d)", a.mode)
	}
	if len(fake.Commands()) != 0 {
		t.Errorf("something ran: %+v", fake.Commands())
	}
	if _, err := a.backend.BuildProvision(directory.Provision{
		Realm: "two.example", NetBIOS: "TWO",
		DNSBackend: directory.DNSBackendInternal,
	}); err == nil {
		t.Error("the backend built a provision over an existing domain")
	}
}

// TestWizardCancelRunsNothing: esc at any step leaves the machine untouched.
func TestWizardCancelRunsNothing(t *testing.T) {
	a, fake := newFreshTestApp(t)
	press(t, a, "P")
	typeInto(t, a, "corp.internal")
	press(t, a, "enter")
	press(t, a, "esc")
	if a.mode != modeBrowse {
		t.Fatalf("esc did not close the wizard (mode %d)", a.mode)
	}
	if len(fake.Commands()) != 0 {
		t.Errorf("a cancelled wizard ran %+v", fake.Commands())
	}
}

// TestPolicyEditEndToEnd drives one password-policy change: the row, the
// prompt, the previewed command, and the reloaded value.
func TestPolicyEditEndToEnd(t *testing.T) {
	a, fake := newTestApp(t)
	a.screen = directory.ScreenDomain
	a.applyFilter()

	if !a.model.Policy.Read {
		t.Fatal("the demo domain's password policy was not read")
	}
	found := false
	for i, row := range a.factRows {
		if row.policy == "min-pwd-length" {
			a.cursor[a.screen] = i
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no policy row for min-pwd-length: %+v", a.factRows)
	}

	press(t, a, "e")
	if a.mode != modeInput {
		t.Fatalf("e did not open the value prompt (mode %d)", a.mode)
	}
	typeInto(t, a, "12")
	press(t, a, "enter")
	if a.mode != modeConfirm {
		t.Fatalf("the prompt did not lead to the confirm dialog (mode %d)", a.mode)
	}
	want := "samba-tool domain passwordsettings set --min-pwd-length=12"
	if a.confirm.Command != want {
		t.Fatalf("the dialog shows %q\n want %q", a.confirm.Command, want)
	}
	press(t, a, "y")
	ran := fake.Commands()
	if len(ran) != 1 || ran[0].String() != want {
		t.Fatalf("ran %+v, want exactly the previewed command", ran)
	}
	for _, setting := range a.model.Policy.Settings {
		if setting.Name == "min-pwd-length" && setting.Value != "12" {
			t.Errorf("the reloaded policy says %q", setting.Value)
		}
	}
}

// TestPolicyEditNeedsAPolicyRow: on a fact row that is not a setting, e says
// so and builds nothing.
func TestPolicyEditNeedsAPolicyRow(t *testing.T) {
	a, fake := newTestApp(t)
	a.screen = directory.ScreenDomain
	a.applyFilter()
	a.cursor[a.screen] = 0 // the realm row
	press(t, a, "e")
	if a.mode != modeBrowse {
		t.Fatalf("e opened a dialog on a non-policy row (mode %d)", a.mode)
	}
	if len(fake.Commands()) != 0 {
		t.Errorf("something ran: %+v", fake.Commands())
	}
}

// TestPolicyEditRejectsTheWrongShape: a word where a number goes is refused
// at build time, with nothing run.
func TestPolicyEditRejectsTheWrongShape(t *testing.T) {
	a, fake := newTestApp(t)
	a.screen = directory.ScreenDomain
	a.applyFilter()
	for i, row := range a.factRows {
		if row.policy == "min-pwd-length" {
			a.cursor[a.screen] = i
			break
		}
	}
	press(t, a, "e")
	typeInto(t, a, "ten")
	press(t, a, "enter")
	if a.mode == modeConfirm {
		t.Fatal("an invalid value reached the confirm dialog")
	}
	if len(fake.Commands()) != 0 {
		t.Errorf("something ran: %+v", fake.Commands())
	}
}
