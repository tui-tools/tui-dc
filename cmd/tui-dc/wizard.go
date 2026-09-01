package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-dc/internal/samba"
	"github.com/tui-tools/tui-kit/runner"
	"github.com/tui-tools/tui-kit/ui"
)

// The provision wizard. Creating a domain needs four answers and one act of
// deliberation, which is more than one prompt carries — so it is a small
// state machine of the same dialogs every other action uses, ending in the
// same confirm dialog every other change ends in. Nothing here runs anything:
// the wizard's whole output is one previewed runner.Command.

// wizardStep is where the wizard is.
type wizardStep int

const (
	wizRealm wizardStep = iota
	wizNetBIOS
	wizBackend
	wizForwarder
	// wizTyped is the deliberate step: the realm, typed back in full. A
	// provision replaces nothing today but decides everything after, so it
	// gets the confirmation a destructive action gets, twice.
	wizTyped
)

// wizardState carries the wizard between keystrokes.
type wizardState struct {
	step   wizardStep
	p      directory.Provision
	input  ui.Input
	picker ui.Picker
}

// provisionOffered reports whether this machine is one the wizard is for: a
// samba-tool with no domain behind it. A broken controller (role says DC,
// nothing answers) is deliberately not offered a provision — that machine
// needs repair, not a second domain.
func (a *app) provisionOffered() bool {
	return a.model.Installed && !a.loadFailed && !a.model.Domain.IsDC()
}

// startWizard opens the wizard at its first question.
func (a *app) startWizard() {
	if !a.provisionOffered() {
		a.setStatus(ui.StatusWarn,
			"this host already serves a domain — provisioning over it is refused")
		return
	}
	a.wizard = wizardState{step: wizRealm}
	a.wizard.input = ui.NewInput("Provision 1/5 — realm", "lab.example", "")
	a.wizard.input.Help = "The domain's DNS name, fully qualified. It becomes the " +
		"Kerberos realm and the DNS zone, and it cannot be renamed later."
	a.mode = modeWizard
}

// handleWizard routes a key to whichever dialog the current step is showing.
func (a *app) handleWizard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.wizard.step == wizBackend {
		a.wizard.picker.Update(msg)
		if !a.wizard.picker.Done {
			return a, nil
		}
		if !a.wizard.picker.Accepted {
			return a.cancelWizard()
		}
		a.wizard.p.DNSBackend = a.wizard.picker.Selected()
		return a.wizardAdvance()
	}

	cmd, _ := a.wizard.input.Update(msg)
	if !a.wizard.input.Done {
		return a, cmd
	}
	if !a.wizard.input.Accepted {
		return a.cancelWizard()
	}
	value := a.wizard.input.Value()

	switch a.wizard.step {
	case wizRealm:
		if err := directory.ValidateRealm(value); err != nil {
			return a.wizardRetry(err.Error())
		}
		a.wizard.p.Realm = value
	case wizNetBIOS:
		if err := directory.ValidateNetBIOS(value); err != nil {
			return a.wizardRetry(err.Error())
		}
		a.wizard.p.NetBIOS = value
	case wizForwarder:
		if err := directory.ValidateForwarder(value); err != nil {
			return a.wizardRetry(err.Error())
		}
		a.wizard.p.Forwarder = value
	case wizTyped:
		if !strings.EqualFold(value, a.wizard.p.Realm) {
			return a.wizardRetry("that is not the realm — type " +
				a.wizard.p.Realm + " exactly, or press esc to stop")
		}
		return a.finishWizard()
	}
	return a.wizardAdvance()
}

// wizardAdvance opens the next step's dialog.
func (a *app) wizardAdvance() (tea.Model, tea.Cmd) {
	switch a.wizard.step {
	case wizRealm:
		a.wizard.step = wizNetBIOS
		suggested := directory.DeriveNetBIOS(a.wizard.p.Realm)
		a.wizard.input = ui.NewInput("Provision 2/5 — NetBIOS domain",
			suggested, suggested)
		a.wizard.input.Help = "The short pre-2000 name, at most 15 characters. " +
			"The suggestion is the realm's first label."
	case wizNetBIOS:
		a.wizard.step = wizBackend
		a.wizard.picker = ui.NewPicker("Provision 3/5 — DNS backend",
			directory.DNSBackends(), directory.DNSBackendInternal)
	case wizBackend:
		if a.wizard.p.DNSBackend != directory.DNSBackendInternal {
			// A forwarder only means something to the internal DNS server;
			// with BIND the question would be a trap.
			a.wizard.p.Forwarder = ""
			a.wizard.step = wizForwarder
			return a.wizardAdvance()
		}
		a.wizard.step = wizForwarder
		a.wizard.input = ui.NewInput("Provision 4/5 — DNS forwarder (optional)",
			"10.0.0.1", "")
		a.wizard.input.Help = "Where the internal DNS server sends queries it is " +
			"not authoritative for. Empty means no forwarder."
	case wizForwarder:
		a.wizard.step = wizTyped
		a.wizard.input = ui.NewInput("Provision 5/5 — type the realm to continue",
			a.wizard.p.Realm, "")
		a.wizard.input.Help = "Provisioning creates a domain this host will serve " +
			"from now on. Type " + a.wizard.p.Realm + " to see the exact command."
	}
	return a, nil
}

// wizardRetry re-opens the current step with the reason it was refused. The
// deliberate step starts over blank: a half-right realm is not something to
// edit toward, it is something to type again.
func (a *app) wizardRetry(reason string) (tea.Model, tea.Cmd) {
	a.setStatus(ui.StatusError, reason)
	a.wizard.input.Done, a.wizard.input.Accepted = false, false
	if a.wizard.step == wizTyped {
		a.wizard.input.Model.SetValue("")
	}
	a.wizard.input.Model.Focus()
	return a, nil
}

// cancelWizard returns to the browse screen with nothing built.
func (a *app) cancelWizard() (tea.Model, tea.Cmd) {
	a.wizard = wizardState{}
	a.mode = modeBrowse
	a.setStatus(ui.StatusInfo, "cancelled")
	return a, nil
}

// finishWizard builds the one command the wizard exists for and hands it to
// the same confirm dialog every other change goes through.
func (a *app) finishWizard() (tea.Model, tea.Cmd) {
	cmd, err := a.backend.BuildProvision(a.wizard.p)
	a.wizard = wizardState{}
	if err != nil {
		a.mode = modeBrowse
		a.setStatus(ui.StatusError, err.Error())
		return a, nil
	}
	a.mode = modeConfirm
	a.confirm = ui.Confirm{
		Title: cmd.Description,
		Body: "This host becomes the first domain controller of a new domain. " +
			"There is no --adminpass on this command line — samba-tool generates " +
			"the Administrator password itself and prints it exactly once when " +
			"provisioning finishes. This can take minutes.",
		Command: a.backend.Preview(cmd),
		Danger:  true,
		Payload: cmd,
	}
	return a, nil
}

// wizardView renders whichever dialog the current step is showing.
func (a *app) wizardView() string {
	if a.wizard.step == wizBackend {
		return a.wizard.picker.View(a.theme, a.width, a.height)
	}
	return a.wizard.input.View(a.theme, a.width, a.height)
}

// noticeState is the result screen a provision ends on: the one-time facts a
// user must not lose, and the next step when there is one.
type noticeState struct {
	title string
	lines []string
	// next is the previewed follow-up command (systemctl enable --now …),
	// offered when the notice closes.
	next *runner.Command
}

// provisionNotice builds the result screen from the provision transcript.
func (a *app) provisionNotice(output string) noticeState {
	result := samba.ParseProvisionOutput(output)
	notice := noticeState{title: "The domain is provisioned"}
	add := func(lines ...string) { notice.lines = append(notice.lines, lines...) }

	if result.AdminPassword != "" {
		add("Administrator password — shown once, by samba-tool, never stored:",
			"", "    "+result.AdminPassword, "")
	} else {
		add("samba-tool did not print an Admin password line; if you passed no",
			"password it may have failed — read the transcript in the status line.", "")
	}
	if len(result.Summary) > 0 {
		add(result.Summary...)
		add("")
	}
	if result.Krb5Conf != "" {
		add("A Kerberos configuration was generated at "+result.Krb5Conf+";",
			"merge it into /etc/krb5.conf (do not symlink it).", "")
	}
	if cmd, ok := a.backend.EnableServiceCommand(); ok {
		notice.next = &cmd
		add("The directory exists but nothing serves it yet. Enter previews the",
			"command that starts it: "+cmd.String())
	} else {
		add("The directory exists but nothing serves it yet. Start it with your",
			"init system (samba.service or samba-ad-dc.service, by distribution).")
	}
	return notice
}

// handleNotice closes the result screen, into the follow-up confirm when
// there is one.
func (a *app) handleNotice(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		notice := a.notice
		a.notice = noticeState{}
		a.mode = modeBrowse
		if notice.next != nil {
			cmd := *notice.next
			a.mode = modeConfirm
			a.confirm = ui.Confirm{
				Title: cmd.Description,
				Body: "The unit is enabled so the controller survives a reboot, " +
					"and started now.",
				Command: a.backend.Preview(cmd),
				Payload: cmd,
			}
		}
	case "esc", "q":
		a.notice = noticeState{}
		a.mode = modeBrowse
	}
	return a, nil
}

// noticeView renders the result screen.
func (a *app) noticeView() string {
	lines := []string{a.theme.Title.Render(a.notice.title), ""}
	for _, line := range a.notice.lines {
		lines = append(lines, a.theme.Base.Render(line))
	}
	lines = append(lines, "",
		a.theme.Key.Render("enter")+a.theme.KeyDesc.Render(" continue    ")+
			a.theme.Key.Render("esc")+a.theme.KeyDesc.Render(" close"))
	box := a.theme.Dialog.MaxWidth(max(a.width-4, 20)).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, box)
}
