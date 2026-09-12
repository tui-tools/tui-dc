package main

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-dc/internal/samba"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/runner"
	"github.com/tui-tools/tui-kit/theme"
	"github.com/tui-tools/tui-kit/ui"
)

// twoAddressHost is the host the address step exists for: the address it is
// reached on, and a bridge. The wizard's address reads are stubbed with it so
// no test depends on the interfaces of the machine running it.
func twoAddressHost() []directory.HostAddress {
	return []directory.HostAddress{
		{IP: "192.168.10.20", Iface: "eth0"},
		{IP: "192.168.122.1", Iface: "virbr0"},
	}
}

// stubHostAddresses holds a set of addresses still for one test.
func stubHostAddresses(t *testing.T, addrs []directory.HostAddress) {
	t.Helper()
	previous := readHostAddresses
	readHostAddresses = func() []directory.HostAddress { return addrs }
	t.Cleanup(func() { readHostAddresses = previous })
}

// clearPreflight walks the preflight the fresh fake machine reports — the
// distribution's smb.conf in provision's way — and leaves the app on the browse
// screen with nothing in the way of the wizard.
func clearPreflight(t *testing.T, a *app) {
	t.Helper()
	press(t, a, "P")
	if a.mode != modeNotice {
		t.Fatalf("P did not open the preflight screen (mode %d)", a.mode)
	}
	press(t, a, "enter")
	if a.mode != modeConfirm {
		t.Fatalf("the preflight did not offer its command (mode %d)", a.mode)
	}
	press(t, a, "y")
	if a.mode != modeNotice {
		t.Fatalf("the preflight screen did not come back (mode %d)", a.mode)
	}
	press(t, a, "enter")
	if a.mode != modeBrowse {
		t.Fatalf("the preflight screen did not close (mode %d)", a.mode)
	}
}

// provisionThroughWizard walks the wizard with the answers the end-to-end test
// checks one at a time, and confirms the provision. It is here for the tests
// that are about what happens *after* a provision: they need a provisioned fake
// machine, and the six answers that get there are not what they are proving.
func provisionThroughWizard(t *testing.T, a *app) {
	t.Helper()
	press(t, a, "P")
	if a.mode != modeWizard {
		t.Fatalf("P did not open the wizard (mode %d)", a.mode)
	}
	typeInto(t, a, "corp.internal")
	press(t, a, "enter") // realm
	press(t, a, "enter") // the suggested NetBIOS name
	press(t, a, "enter") // SAMBA_INTERNAL
	press(t, a, "enter") // no forwarder
	press(t, a, "enter") // the preselected address
	typeInto(t, a, "corp.internal")
	press(t, a, "enter") // the typed-realm gate
	if a.mode != modeConfirm {
		t.Fatalf("the wizard did not end in the confirm dialog (mode %d)", a.mode)
	}
	press(t, a, "y")
	if a.mode != modeNotice {
		t.Fatalf("the provision did not end on the result screen (mode %d)", a.mode)
	}
}

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
	stubHostAddresses(t, twoAddressHost())

	// The offer is on the screen, not only in the help.
	if !strings.Contains(a.View(), "press P to provision") {
		t.Error("the domain screen does not offer the wizard")
	}

	clearPreflight(t, a)
	// The preflight's previewed move is already on the record, so everything
	// below counts the commands the wizard itself caused.
	cleared := len(fake.Commands())
	if cleared != 1 {
		t.Fatalf("the preflight ran %+v, want exactly the move", fake.Commands())
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
	// Step 5: the address, with the one on the default route preselected and
	// the second one a keystroke away.
	if a.wizard.step != wizAddress {
		t.Fatalf("the wizard did not reach the address step (step %d)", a.wizard.step)
	}
	if got := a.wizard.picker.Selected(); got != "192.168.10.20 on eth0" {
		t.Errorf("the picker opened on %q, not on the default route's address", got)
	}
	press(t, a, "down")
	press(t, a, "enter")
	if a.wizard.p.HostIP != "192.168.122.1" || a.wizard.p.Iface != "virbr0" {
		t.Fatalf("the address step collected %q on %q",
			a.wizard.p.HostIP, a.wizard.p.Iface)
	}
	// Step 6: the typed-realm gate refuses anything but the realm.
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
	// The forwarder is an smb.conf parameter, not a provision flag, so it
	// travels as one --option argument whose parameter name carries a space.
	// The argv keeps that space and the preview quotes it, so what the dialog
	// shows is still a line a reader could paste into a shell.
	wantArgv := "samba-tool domain provision --realm=CORP.INTERNAL --domain=CORP " +
		"--server-role=dc --dns-backend=SAMBA_INTERNAL " +
		"--host-ip=192.168.122.1 --option=interfaces=lo virbr0 " +
		"--option=bind interfaces only=yes --option=dns forwarder=10.0.0.1"
	wantPreview := "samba-tool domain provision --realm=CORP.INTERNAL --domain=CORP " +
		"--server-role=dc --dns-backend=SAMBA_INTERNAL " +
		"--host-ip=192.168.122.1 '--option=interfaces=lo virbr0' " +
		"'--option=bind interfaces only=yes' '--option=dns forwarder=10.0.0.1'"
	if a.confirm.Command != wantPreview {
		t.Fatalf("the dialog shows %q\n want %q", a.confirm.Command, wantPreview)
	}
	if !a.confirm.Danger {
		t.Error("a provision must be painted as destructive")
	}
	if strings.Contains(a.confirm.Command, "adminpass") {
		t.Error("an adminpass reached the previewed command line")
	}
	if len(fake.Commands()) != cleared {
		t.Fatal("something ran before the dialog was answered")
	}

	press(t, a, "y")
	ran := fake.Commands()
	if len(ran) != cleared+1 || ran[cleared].String() != wantArgv {
		t.Fatalf("ran %+v, want exactly the previewed command", ran)
	}
	// Nothing but quoting separates the two: the preview renders the argv that
	// ran, it does not build a second command line.
	if a.backend.Preview(ran[cleared]) != wantPreview {
		t.Errorf("the command that ran previews as %q", a.backend.Preview(ran[cleared]))
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
	// The warnings provision printed are on the screen: they are facts about
	// the domain that was just created and are shown nowhere else. The address
	// was answered, so samba had nothing to choose and said nothing about it.
	if !strings.Contains(view, "No IPv6 address will be assigned") {
		t.Error("the result screen does not show samba's warnings")
	}
	if strings.Contains(view, "More than one IPv4 address") {
		t.Error("a provision that was told its address still warned about one")
	}
	if !a.model.Domain.IsDC() || a.model.Domain.Realm != "corp.internal" {
		t.Errorf("the reload under the notice did not see the domain: %+v",
			a.model.Domain)
	}

	// The follow-ups are a chain, in the order that ends with a running DC: the
	// Kerberos drop-in first, because on a samba built against the MIT KDC the
	// unit does not start without it; then the distribution's own file server,
	// because it holds the ports the controller's own smbd has to bind; and the
	// unit last. The fake machine has both conditions on purpose, so one demo
	// and one test walk both halves of what the lab matrix found.
	press(t, a, "enter")
	if a.mode != modeConfirm {
		t.Fatalf("the notice did not offer the Kerberos step (mode %d)", a.mode)
	}
	wantKrb5 := "install -m 644 /var/lib/samba/private/krb5.conf " +
		"/etc/krb5.conf.d/samba-dc.conf"
	if a.confirm.Command != wantKrb5 {
		t.Fatalf("the first step shows %q\n want %q", a.confirm.Command, wantKrb5)
	}
	press(t, a, "y")
	if a.mode != modeNotice {
		t.Fatalf("the screen did not come back with the step that was left "+
			"(mode %d)", a.mode)
	}

	press(t, a, "enter")
	if a.mode != modeConfirm {
		t.Fatalf("the notice did not offer the file server step (mode %d)", a.mode)
	}
	wantQuiesce := "systemctl disable --now smbd.service nmbd.service"
	if a.confirm.Command != wantQuiesce {
		t.Fatalf("the second step shows %q\n want %q", a.confirm.Command, wantQuiesce)
	}
	if !a.confirm.Danger {
		t.Error("stopping a running file server was not offered as a change to weigh")
	}
	press(t, a, "y")
	if a.mode != modeNotice {
		t.Fatalf("the screen did not come back with the step that was left "+
			"(mode %d)", a.mode)
	}

	press(t, a, "enter")
	if a.mode != modeConfirm {
		t.Fatalf("the notice did not offer the service step (mode %d)", a.mode)
	}
	if a.confirm.Command != "systemctl enable --now samba-ad-dc.service" {
		t.Fatalf("the service dialog shows %q", a.confirm.Command)
	}
	press(t, a, "y")
	ran = fake.Commands()
	if got := ran[len(ran)-1].String(); got !=
		"systemctl enable --now samba-ad-dc.service" {
		t.Errorf("the service step ran %q", got)
	}
	if got := ran[len(ran)-2].String(); got != wantQuiesce {
		t.Errorf("the step before the unit was %q, not the file server one", got)
	}
	if got := ran[len(ran)-3].String(); got != wantKrb5 {
		t.Errorf("the step before that was %q, not the Kerberos drop-in", got)
	}
	// And the unit started, which on this fake machine it only does once the
	// generated Kerberos configuration is in place and the file server is not.
	if a.statusKind == ui.StatusError {
		t.Errorf("the unit did not start: %s", a.status)
	}
}

// TestTheUnitIsNotOfferedBeforeTheStepsItNeeds is the order as a refusal rather
// than as a sequence: confirming the unit step while either prerequisite is
// outstanding is what used to happen on a real host, and what the journal lines
// in issues #12 and #18 are. The fake answers both the way systemd did.
func TestTheUnitIsNotOfferedBeforeTheStepsItNeeds(t *testing.T) {
	a, fake := newFreshTestApp(t)
	stubHostAddresses(t, twoAddressHost())
	clearPreflight(t, a)
	provisionThroughWizard(t, a)

	// The Kerberos step, which the chain offers first and which the fake needs
	// before it will discuss the unit at all — that refusal is issue #12's and is
	// already held elsewhere.
	press(t, a, "enter")
	press(t, a, "y")

	// From here the only thing left between the provision and a serving
	// controller is the file server. Skipping to the unit step is not something
	// the screen offers, so the test asks the backend for it and runs it the way
	// a confirm would.
	unit, ok := a.backend.EnableServiceCommand()
	if !ok {
		t.Fatal("the fake machine offered no unit step")
	}
	if _, err := a.backend.Run(t.Context(), unit); err == nil {
		t.Error("the unit started with the file server still holding 139 and 445")
	} else if !strings.Contains(err.Error(), "smbd child process exited") {
		t.Errorf("it failed for another reason: %v", err)
	}

	quiesce, ok := a.backend.FileServerDisableCommand()
	if !ok {
		t.Fatal("the fake machine offered no file server step")
	}
	if _, err := a.backend.Run(t.Context(), quiesce); err != nil {
		t.Fatalf("the file server step failed: %v", err)
	}
	// Once it has run it is not offered again: the units are gone from the boot.
	if _, ok := a.backend.FileServerDisableCommand(); ok {
		t.Error("the file server step is still offered after it ran")
	}
	if _, err := a.backend.Run(t.Context(), unit); err != nil {
		t.Errorf("the unit still did not start: %v", err)
	}
	// Every command that reached the machine was previewed first: the fake runs
	// them through the same runner hook the real backend runs them through.
	if len(fake.Commands()) == 0 {
		t.Error("nothing was recorded as run")
	}
}

// TestPreflightNamesBothConditionsAndOffersOnlyWhatItCanRun is the preflight
// from the UI's side: both conditions on the screen, one previewed command for
// the one the tool can clear, and nothing offered for the missing package.
func TestPreflightNamesBothConditionsAndOffersOnlyWhatItCanRun(t *testing.T) {
	a, fake := newFreshTestApp(t)
	a.backend = &preflightBackend{
		Backend: a.backend,
		preflight: directory.Preflight{Conditions: []directory.PreflightCondition{
			{
				Title:   "/etc/samba/smb.conf configures this host as auto",
				Detail:  []string{"provision will not start beside it."},
				Fix:     &runner.Command{Argv: []string{"mv", "/etc/samba/smb.conf", "/etc/samba/smb.conf.orig"}, Description: "Move /etc/samba/smb.conf aside"},
				FixBody: "provision writes its own.",
			},
			{
				Title:  "the AD provisioning data is not installed",
				Detail: []string{"install samba-dc-provision and samba-dc."},
			},
		}},
	}

	press(t, a, "P")
	if a.mode != modeNotice {
		t.Fatalf("the preflight did not open (mode %d)", a.mode)
	}
	view := a.View()
	for _, want := range []string{
		"/etc/samba/smb.conf", "AD provisioning data", "samba-dc-provision",
		"mv /etc/samba/smb.conf /etc/samba/smb.conf.orig",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the preflight screen does not mention %q", want)
		}
	}
	if len(a.notice.steps) != 1 {
		t.Fatalf("steps = %+v", a.notice.steps)
	}
	if len(fake.Commands()) != 0 {
		t.Errorf("the preflight ran something: %+v", fake.Commands())
	}

	// Cancelling the one offered command keeps the screen and runs nothing.
	press(t, a, "enter")
	press(t, a, "n")
	if a.mode != modeNotice {
		t.Errorf("a cancelled step closed the screen (mode %d)", a.mode)
	}
	if len(fake.Commands()) != 0 {
		t.Errorf("a cancelled step ran: %+v", fake.Commands())
	}
}

// TestALongNoticeKeepsItsTitle is what the Ubuntu lab run found: a preflight
// naming several conditions is taller than a 44-row terminal, and a box centred
// in a screen it does not fit loses rows off the top — the title first. There is
// no scrolling on this screen, so the assertion is about which end survives.
func TestALongNoticeKeepsItsTitle(t *testing.T) {
	a, _ := newFreshTestApp(t)
	a.height = 20
	var detail []string
	for i := 0; i < 40; i++ {
		detail = append(detail, "a line of detail, number "+strconv.Itoa(i))
	}
	a.backend = &preflightBackend{
		Backend: a.backend,
		preflight: directory.Preflight{Conditions: []directory.PreflightCondition{
			{Title: "something is wrong with this host", Detail: detail},
		}},
	}

	press(t, a, "P")
	if a.mode != modeNotice {
		t.Fatalf("the preflight did not open (mode %d)", a.mode)
	}
	view := a.View()
	if !strings.Contains(view, "Provisioning cannot start on this host yet") {
		t.Error("a notice taller than the screen lost its title")
	}
	if !strings.Contains(view, "something is wrong with this host") {
		t.Error("and the first condition with it")
	}
}

// preflightBackend is a backend whose preflight is fixed, so the screen can be
// driven through states a fake machine does not have to be able to reach.
type preflightBackend struct {
	directory.Backend
	preflight directory.Preflight
}

func (p *preflightBackend) ProvisionPreflight(string) directory.Preflight {
	return p.preflight
}

// TestWizardSkipsTheAddressQuestionOnASingleAddressHost: a question with one
// answer is noise, and the answer is still collected.
func TestWizardSkipsTheAddressQuestionOnASingleAddressHost(t *testing.T) {
	a, fake := newFreshTestApp(t)
	stubHostAddresses(t, []directory.HostAddress{
		{IP: "192.168.10.20", Iface: "eth0"},
	})
	clearPreflight(t, a)

	press(t, a, "P")
	typeInto(t, a, "corp.internal")
	press(t, a, "enter") // realm
	press(t, a, "enter") // the suggested NetBIOS name
	press(t, a, "enter") // the default DNS backend
	press(t, a, "enter") // no forwarder
	if a.wizard.step != wizTyped {
		t.Fatalf("the wizard stopped on a question with one answer (step %d)",
			a.wizard.step)
	}
	typeInto(t, a, "corp.internal")
	press(t, a, "enter")
	if a.mode != modeConfirm {
		t.Fatalf("the wizard did not reach the confirm dialog (mode %d)", a.mode)
	}
	want := "samba-tool domain provision --realm=CORP.INTERNAL --domain=CORP " +
		"--server-role=dc --dns-backend=SAMBA_INTERNAL " +
		"--host-ip=192.168.10.20 '--option=interfaces=lo eth0' " +
		"'--option=bind interfaces only=yes'"
	if a.confirm.Command != want {
		t.Fatalf("the dialog shows %q\n want %q", a.confirm.Command, want)
	}
	// Only the preflight's move has run: the wizard itself never runs anything.
	if ran := fake.Commands(); len(ran) != 1 || ran[0].Argv[0] != "mv" {
		t.Errorf("ran %+v before the dialog was answered", ran)
	}
}

// TestWizardWithoutAnyAddressLeavesTheChoiceToSamba: on a host with no
// serviceable address there is nothing to answer with, and the command is the
// one this tool has always built.
func TestWizardWithoutAnyAddressLeavesTheChoiceToSamba(t *testing.T) {
	a, _ := newFreshTestApp(t)
	stubHostAddresses(t, nil)
	clearPreflight(t, a)

	press(t, a, "P")
	typeInto(t, a, "corp.internal")
	press(t, a, "enter")
	press(t, a, "enter")
	press(t, a, "enter")
	press(t, a, "enter")
	typeInto(t, a, "corp.internal")
	press(t, a, "enter")
	if a.mode != modeConfirm {
		t.Fatalf("the wizard did not reach the confirm dialog (mode %d)", a.mode)
	}
	if strings.Contains(a.confirm.Command, "--host-ip") ||
		strings.Contains(a.confirm.Command, "interfaces") {
		t.Errorf("an address was invented: %q", a.confirm.Command)
	}
}

// TestFailedProvisionKeepsItsTranscript: a provision prints hundreds of lines
// before it fails, and the reason is in them. Reduced to a status line it was
// unreadable, which is what made the two preflight conditions so hard to
// diagnose in the first place.
func TestFailedProvisionKeepsItsTranscript(t *testing.T) {
	a, _ := newFreshTestApp(t)
	transcript := strings.Join([]string{
		"INFO … #1520: Setting up SAM db",
		"INFO … #1612: Pre-loading the Samba 4 and AD schema",
		"ERROR(<class 'FileNotFoundError'>): uncaught exception - [Errno 2] No " +
			"such file or directory: '/usr/share/samba/setup/ad-schema/" +
			"AD_DS_Attributes_Windows_Server_v1903.ldf'",
		"  File \"/usr/lib64/python3.14/site-packages/samba/schema.py\", line 118",
	}, "\n")

	cmd, err := directory.BuildProvisionCommand(directory.Provision{
		Realm: "corp.internal", NetBIOS: "CORP",
		DNSBackend: directory.DNSBackendInternal,
	})
	if err != nil {
		t.Fatalf("BuildProvisionCommand: %v", err)
	}
	a.Update(ranMsg{cmd: cmd, output: transcript,
		err: errors.New("`samba-tool domain provision …` failed: ERROR(<class " +
			"'FileNotFoundError'>): uncaught exception")})

	if a.mode != modeNotice {
		t.Fatalf("a failed provision did not get the result screen (mode %d)", a.mode)
	}
	view := a.View()
	for _, want := range []string{
		"The provision failed",
		"AD_DS_Attributes_Windows_Server_v1903.ldf",
		"Pre-loading the Samba 4 and AD schema",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the failure screen does not show %q", want)
		}
	}
	if len(a.notice.steps) != 0 {
		t.Errorf("a failed provision offered follow-up steps: %+v", a.notice.steps)
	}
}

// TestTranscriptTailKeepsTheEnd: the reason a provision failed is the last
// thing it printed, so the tail is what is kept.
func TestTranscriptTailKeepsTheEnd(t *testing.T) {
	var lines []string
	for i := range 40 {
		lines = append(lines, "line "+strconv.Itoa(i))
	}
	tail := transcriptTail(strings.Join(lines, "\n") + "\n\n   \n")
	if len(tail) != transcriptTailLines {
		t.Fatalf("kept %d lines", len(tail))
	}
	if tail[len(tail)-1] != "line 39" {
		t.Errorf("the last kept line is %q", tail[len(tail)-1])
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
	stubHostAddresses(t, twoAddressHost())
	clearPreflight(t, a)
	cleared := len(fake.Commands())
	press(t, a, "P")
	typeInto(t, a, "corp.internal")
	press(t, a, "enter")
	press(t, a, "esc")
	if a.mode != modeBrowse {
		t.Fatalf("esc did not close the wizard (mode %d)", a.mode)
	}
	if len(fake.Commands()) != cleared {
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
