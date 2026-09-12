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
	// wizAddress is which of this host's IPv4 addresses the controller serves
	// on. It is skipped where there is only one, because a question with one
	// answer is noise.
	wizAddress
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
	// addrs are the addresses the picker is offering, kept so the selected
	// label can be turned back into an address and the interface that owns it.
	addrs []directory.HostAddress
}

// readHostAddresses reads this host's serviceable IPv4 addresses. It is a
// variable so the wizard's tests can hold a set of addresses still: a test
// whose expectations depended on the machine running it would pass or fail by
// accident of where it ran.
var readHostAddresses = directory.HostAddresses

// provisionOffered reports whether this machine is one the wizard is for: a
// samba-tool with no domain behind it. A broken controller (role says DC,
// nothing answers) is deliberately not offered a provision — that machine
// needs repair, not a second domain.
func (a *app) provisionOffered() bool {
	return a.model.Installed && !a.loadFailed && !a.model.Domain.IsDC()
}

// startWizard runs the preflight and, when nothing is in the way, opens the
// wizard at its first question.
//
// The preflight comes first because both of the conditions it finds used to be
// discovered after the realm had been typed twice and the command confirmed —
// one of them only after a provision had run for a minute and died in a Python
// traceback. A host where nothing is wrong sees none of this.
func (a *app) startWizard() {
	if !a.provisionOffered() {
		a.setStatus(ui.StatusWarn,
			"this host already serves a domain — provisioning over it is refused")
		return
	}
	if preflight := a.backend.ProvisionPreflight(a.model.Domain.ServerRole); !preflight.OK() {
		a.notice = a.preflightNotice(preflight)
		a.mode = modeNotice
		return
	}
	a.wizard = wizardState{step: wizRealm}
	a.wizard.input = ui.NewInput("Provision 1/6 — realm", "lab.example", "")
	a.wizard.input.Help = "The domain's DNS name, fully qualified. It becomes the " +
		"Kerberos realm and the DNS zone, and it cannot be renamed later."
	a.mode = modeWizard
}

// handleWizard routes a key to whichever dialog the current step is showing.
func (a *app) handleWizard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.wizard.step == wizBackend || a.wizard.step == wizAddress {
		a.wizard.picker.Update(msg)
		if !a.wizard.picker.Done {
			return a, nil
		}
		if !a.wizard.picker.Accepted {
			return a.cancelWizard()
		}
		if a.wizard.step == wizBackend {
			a.wizard.p.DNSBackend = a.wizard.picker.Selected()
			return a.wizardAdvance()
		}
		// The picker deals in labels; this is where one becomes an address and
		// the interface that owns it. Both came from this host, so a selection
		// that does not match one is impossible rather than invalid.
		addr, ok := directory.FindHostAddress(a.wizard.addrs, a.wizard.picker.Selected())
		if !ok {
			return a.cancelWizard()
		}
		a.wizard.p.HostIP, a.wizard.p.Iface = addr.IP, addr.Iface
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
		a.wizard.input = ui.NewInput("Provision 2/6 — NetBIOS domain",
			suggested, suggested)
		a.wizard.input.Help = "The short pre-2000 name, at most 15 characters. " +
			"The suggestion is the realm's first label."
	case wizNetBIOS:
		a.wizard.step = wizBackend
		a.wizard.picker = ui.NewPicker("Provision 3/6 — DNS backend",
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
		a.wizard.input = ui.NewInput("Provision 4/6 — DNS forwarder (optional)",
			"10.0.0.1", "")
		a.wizard.input.Help = "Where the internal DNS server sends queries it is " +
			"not authoritative for. Empty means no forwarder."
	case wizForwarder:
		a.wizard.step = wizAddress
		a.wizard.addrs = readHostAddresses()
		if len(a.wizard.addrs) < 2 {
			// One address is not a question, and none means there is nothing to
			// answer with: samba's own guess is then the only address there is.
			if len(a.wizard.addrs) == 1 {
				a.wizard.p.HostIP = a.wizard.addrs[0].IP
				a.wizard.p.Iface = a.wizard.addrs[0].Iface
			}
			return a.wizardAdvance()
		}
		a.wizard.picker = ui.NewPicker("Provision 5/6 — the address this host serves",
			directory.HostAddressLabels(a.wizard.addrs), a.wizard.addrs[0].Label())
	case wizAddress:
		a.wizard.step = wizTyped
		a.wizard.input = ui.NewInput("Provision 6/6 — type the realm to continue",
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
	p := a.wizard.p
	cmd, err := a.backend.BuildProvision(p)
	a.wizard = wizardState{}
	if err != nil {
		a.mode = modeBrowse
		a.setStatus(ui.StatusError, err.Error())
		return a, nil
	}
	a.mode = modeConfirm
	a.confirm = ui.Confirm{
		Title:   cmd.Description,
		Body:    provisionBody(p),
		Command: a.backend.Preview(cmd),
		Danger:  true,
		Payload: cmd,
	}
	return a, nil
}

// provisionBody is what the confirm dialog says about the command it is
// showing. The address and the binding are explained here rather than left to
// be read off two --option arguments, because they are the two answers that
// decide whether the controller can serve anything.
func provisionBody(p directory.Provision) string {
	body := "This host becomes the first domain controller of a new domain. " +
		"There is no --adminpass on this command line — samba-tool generates " +
		"the Administrator password itself and prints it exactly once when " +
		"provisioning finishes. This can take minutes."
	if p.HostIP == "" {
		return body
	}
	body += "\n\nThe domain controller answers on " + p.HostIP +
		", and that is the address its own A record carries — the record every " +
		"member of the domain resolves."
	if p.Iface != "" {
		body += " It is bound to " + p.Iface + " and loopback rather than to " +
			"every address on this host, so the internal DNS server claims port " +
			"53 only there: on a host where libvirt or a docker bridge already " +
			"holds it on another address, a controller that tried all of them " +
			"would never start."
	}
	return body
}

// wizardView renders whichever dialog the current step is showing.
func (a *app) wizardView() string {
	if a.wizard.step == wizBackend || a.wizard.step == wizAddress {
		return a.wizard.picker.View(a.theme, a.width, a.height)
	}
	return a.wizard.input.View(a.theme, a.width, a.height)
}

// noticeStep is one previewed follow-up a notice offers. The steps are ordered,
// and the order is the point: enter offers the first, and the screen comes back
// with the rest still pending, so a chain of two is read and confirmed as two
// commands rather than run as one.
type noticeStep struct {
	cmd runner.Command
	// body is what the confirm dialog says about this step before it runs.
	body string
}

// noticeState is a full-screen result: the facts a user must not lose, and the
// steps that follow from them, in order.
type noticeState struct {
	title string
	lines []string
	steps []noticeStep
}

// open reports that a notice has something to show.
func (n noticeState) open() bool { return n.title != "" }

// preflightNotice is the screen the wizard opens on instead of its first
// question when something would stop a provision. Every condition is named with
// what to do about it; the ones the tool can clear come back as previewed
// steps, and the ones it cannot are stated and left.
func (a *app) preflightNotice(preflight directory.Preflight) noticeState {
	notice := noticeState{title: "Provisioning cannot start on this host yet"}
	add := func(lines ...string) { notice.lines = append(notice.lines, lines...) }

	for i, condition := range preflight.Conditions {
		if i > 0 {
			add("")
		}
		add(condition.Title)
		add(condition.Detail...)
	}
	for _, condition := range preflight.Fixable() {
		notice.steps = append(notice.steps,
			noticeStep{cmd: *condition.Fix, body: condition.FixBody})
	}
	add("")
	switch {
	case len(notice.steps) == 1:
		add("Enter previews the one command that clears this: " +
			notice.steps[0].cmd.String())
	case len(notice.steps) > 1:
		add("Enter previews the commands that clear what can be cleared, in order.")
	default:
		add("There is nothing for this tool to run here. Resolve the above and",
			"press P again.")
	}
	return notice
}

// provisionNotice builds the result screen from the provision transcript —
// including a failed one.
//
// A failed provision gets the same full-screen notice a successful one does,
// with the tail of its transcript, because a provision prints hundreds of lines
// before it fails and the reason is in them. Reduced to a status line it was
// unreadable, and that is the practical reason the two conditions the preflight
// now catches were so hard to diagnose.
func (a *app) provisionNotice(output string, err error) noticeState {
	if err != nil {
		notice := noticeState{title: "The provision failed"}
		notice.lines = append(notice.lines, runner.FirstLine(err.Error()), "")
		if tail := transcriptTail(output); len(tail) > 0 {
			notice.lines = append(notice.lines,
				"The last lines samba-tool printed:", "")
			notice.lines = append(notice.lines, tail...)
			notice.lines = append(notice.lines, "")
		}
		notice.lines = append(notice.lines,
			"Nothing was left running. Fix what the transcript names and press P",
			"again.")
		return notice
	}

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
	if len(result.Warnings) > 0 {
		// These are facts about the domain that was just created, and the only
		// place they are ever shown: whether samba had to choose an address for
		// itself, and whether the controller has an IPv6 address at all.
		add("What provision warned about:")
		for _, warning := range result.Warnings {
			add("    " + warning)
		}
		add("")
	}

	// The Kerberos step first, then the unit — in that order because on a samba
	// built against the MIT KDC the unit does not start without it.
	if cmd, ok := a.backend.Krb5DropInCommand(result.Krb5Conf); ok {
		notice.steps = append(notice.steps,
			noticeStep{cmd: cmd, body: samba.Krb5DropInBody})
		add("The Kerberos configuration provision generated at "+result.Krb5Conf,
			"is not in use yet, and this host reads /etc/krb5.conf.d — so it drops",
			"in without editing anything.")
	} else if result.Krb5Conf != "" {
		add("A Kerberos configuration was generated at "+result.Krb5Conf+";",
			"merge it into /etc/krb5.conf (do not symlink it).")
	}
	add("")
	if cmd, ok := a.backend.EnableServiceCommand(); ok {
		notice.steps = append(notice.steps, noticeStep{
			cmd: cmd,
			body: "The unit is enabled so the controller survives a reboot, " +
				"and started now.",
		})
		add("The directory exists but nothing serves it yet. Enter previews the",
			"steps that start it, in order, one confirm each.")
	} else {
		add("The directory exists but nothing serves it yet. Start it with your",
			"init system (samba.service or samba-ad-dc.service, by distribution).")
	}
	return notice
}

// transcriptTailLines is how much of a failed transcript the screen keeps. It is
// the tail rather than the head: samba prints its progress first and its reason
// last.
const transcriptTailLines = 20

// transcriptTail returns the last lines of a command's output, trimmed.
func transcriptTail(output string) []string {
	var lines []string
	for _, raw := range strings.Split(output, "\n") {
		if line := strings.TrimRight(raw, " \t\r"); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > transcriptTailLines {
		lines = lines[len(lines)-transcriptTailLines:]
	}
	return lines
}

// handleNotice closes the result screen, or offers the next step of its chain.
func (a *app) handleNotice(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if len(a.notice.steps) == 0 {
			a.notice, a.noticeResume = noticeState{}, false
			a.mode = modeBrowse
			return a, nil
		}
		step := a.notice.steps[0]
		a.notice.steps = a.notice.steps[1:]
		// The screen is kept, not closed: whatever the confirm dialog answers,
		// the steps that are still pending have to come back.
		a.noticeResume = true
		a.mode = modeConfirm
		a.confirm = ui.Confirm{
			Title:   step.cmd.Description,
			Body:    step.body,
			Command: a.backend.Preview(step.cmd),
			Danger:  step.cmd.Destructive,
			Payload: step.cmd,
		}
	case "esc", "q":
		a.notice, a.noticeResume = noticeState{}, false
		a.mode = modeBrowse
	}
	return a, nil
}

// noteNoticeResult records on the screen what a step it offered did, so a chain
// of two steps reads as a chain rather than as two disappearing dialogs.
func (a *app) noteNoticeResult(cmd runner.Command, err error) {
	if err != nil {
		a.notice.lines = append(a.notice.lines, "",
			"failed: "+cmd.Description+" — "+runner.FirstLine(err.Error()))
		return
	}
	a.notice.lines = append(a.notice.lines, "", "done: "+cmd.Description)
}

// noticeView renders the result screen.
//
// Every line is rendered to the box's own width rather than into it, so a long
// one wraps instead of being cut off. That matters for exactly one of them: a
// transcript line from a failed provision is where the reason is, and a reason
// clipped at the border is no better than the status line this screen replaced.
func (a *app) noticeView() string {
	box := a.theme.Dialog.MaxWidth(max(a.width-4, 20))
	content := max(a.width-4-a.theme.Dialog.GetHorizontalFrameSize(), 20)

	lines := []string{a.theme.Title.Render(a.notice.title), ""}
	for _, line := range a.notice.lines {
		lines = append(lines, a.theme.Base.Width(content).Render(line))
	}
	next := " close"
	if len(a.notice.steps) > 0 {
		next = " preview the next step"
	}
	lines = append(lines, "",
		a.theme.Key.Render("enter")+a.theme.KeyDesc.Render(next+"    ")+
			a.theme.Key.Render("esc")+a.theme.KeyDesc.Render(" close"))
	return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center,
		box.Render(strings.Join(lines, "\n")))
}
