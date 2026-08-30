package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/runner"
	"github.com/tui-tools/tui-kit/theme"
	"github.com/tui-tools/tui-kit/ui"
)

// mode is the dialog the app currently has open. Only one is open at a time,
// which keeps the update loop flat.
type mode int

const (
	modeBrowse mode = iota
	modeDetail
	modeConfirm
	modeInput
	modeHelp
)

// loadTimeout bounds a whole read. It is generous because samba-tool is a
// Python program run nine times, and a controller that is mid-election is
// slower than one that is not.
const loadTimeout = 3 * time.Minute

// runTimeout bounds one change.
const runTimeout = 60 * time.Second

// detailTimeout bounds one `user show` / `computer show` / `group listmembers`.
const detailTimeout = 30 * time.Second

// factRow is one line of the domain screen: a label and what the domain
// answered for it.
type factRow struct {
	label string
	value string
	// note marks a value that is absent or in doubt, so the screen can colour
	// it without the reader having to know which labels matter.
	note bool
}

// app is the tui-dc Bubble Tea model.
type app struct {
	backend directory.Backend
	theme   theme.Theme
	// backendCompat is what the version probe found, rendered in the header.
	backendCompat compat.Result

	model directory.Model

	// The rows left after the filter, per screen, in display order.
	factRows     []factRow
	userRows     []directory.User
	groupRows    []directory.Group
	computerRows []directory.Computer
	recordRows   []directory.Record
	partRows     []directory.Partition

	// The detail a screen asked for and got. A row that is not in one of
	// these has not been read yet, which is a different thing from having
	// nothing to show — see directory.Details.
	userDetail     map[string]directory.User
	computerDetail map[string]directory.Computer
	groupMembers   map[string][]string
	// detailTried remembers the rows whose detail read failed, so a row the
	// domain will not describe is asked about once rather than forever.
	detailTried map[string]bool
	// detailBusy is set while one on-demand read is in flight, so the tool
	// runs one samba-tool process at a time rather than one per visible row
	// all at once.
	detailBusy bool

	width, height int
	screen        directory.Screen
	// cursor and offset are per screen, so moving between tabs does not lose
	// the row the reader was on.
	cursor [directory.ScreenCount]int
	offset [directory.ScreenCount]int
	filter string

	// detailOffset scrolls the detail screen.
	detailOffset int

	mode    mode
	confirm ui.Confirm
	input   ui.Input
	// pending is the action an open prompt is collecting a value for.
	pending directory.ActionSpec
	// pendingTarget is the row that action applies to, captured when the
	// prompt opened so a reload underneath cannot retarget it.
	pendingTarget directory.Intent

	status     string
	statusKind ui.StatusKind
	loading    bool
	// loadFailed reports that the last Load returned an error, so the empty
	// state does not claim the domain simply has nothing in it.
	loadFailed bool
	// busy blocks input while a command runs.
	busy bool
}

// loadedMsg carries the result of a Load.
type loadedMsg struct {
	model directory.Model
	err   error
}

// detailMsg carries the result of one on-demand read.
type detailMsg struct {
	screen   directory.Screen
	name     string
	user     directory.User
	computer directory.Computer
	members  []string
	err      error
}

// ranMsg carries the result of running a command.
type ranMsg struct {
	cmd    runner.Command
	output string
	err    error
}

// newApp builds the model around a backend.
func newApp(backend directory.Backend, th theme.Theme,
	backendCompat compat.Result) *app {
	a := &app{
		backend:        backend,
		theme:          th,
		backendCompat:  backendCompat,
		width:          100,
		height:         30,
		loading:        true,
		userDetail:     map[string]directory.User{},
		computerDetail: map[string]directory.Computer{},
		groupMembers:   map[string][]string{},
		detailTried:    map[string]bool{},
	}
	if th.Warning != "" {
		a.setStatus(ui.StatusWarn, th.Warning)
	}
	return a
}

// Init starts the first load.
func (a *app) Init() tea.Cmd { return a.load() }

// load reads the domain in the background.
func (a *app) load() tea.Cmd {
	backend := a.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		model, err := backend.Load(ctx)
		return loadedMsg{model: model, err: err}
	}
}

// run executes a confirmed command in the background.
func (a *app) run(cmd runner.Command) tea.Cmd {
	backend := a.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		defer cancel()
		out, err := backend.Run(ctx, cmd)
		return ranMsg{cmd: cmd, output: out, err: err}
	}
}

// readDetail reads the extra a row needs when it is opened. It is one command,
// run once per row and remembered, so scrolling a list does not turn into one
// samba-tool process per keystroke.
func (a *app) readDetail(screen directory.Screen, name string) tea.Cmd {
	backend := a.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), detailTimeout)
		defer cancel()
		msg := detailMsg{screen: screen, name: name}
		switch screen {
		case directory.ScreenUsers:
			msg.user, msg.err = backend.ShowUser(ctx, name)
		case directory.ScreenComputers:
			msg.computer, msg.err = backend.ShowComputer(ctx, name)
		case directory.ScreenGroups:
			msg.members, msg.err = backend.GroupMembers(ctx, name)
		}
		return msg
	}
}

// setStatus records a message for the status line.
func (a *app) setStatus(kind ui.StatusKind, message string) {
	a.status = message
	a.statusKind = kind
}

// setStatusf records a formatted message for the status line.
func (a *app) setStatusf(kind ui.StatusKind, format string, args ...any) {
	a.setStatus(kind, fmt.Sprintf(format, args...))
}

// Update is the main event loop.
func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.clampCursor()
		return a, a.fillVisibleDetail()

	case loadedMsg:
		a.loading = false
		if msg.err != nil {
			a.loadFailed = true
			a.setStatus(ui.StatusError, msg.err.Error())
			return a, nil
		}
		a.loadFailed = false
		a.model = msg.model
		// A reload invalidates what was read about single rows: an account
		// that was just disabled must not keep showing the state it had.
		a.userDetail = map[string]directory.User{}
		a.computerDetail = map[string]directory.Computer{}
		a.groupMembers = map[string][]string{}
		a.detailTried = map[string]bool{}
		a.applyFilter()
		if !a.model.Installed && a.model.Detail != "" {
			a.setStatus(ui.StatusWarn, a.model.Detail)
		}
		return a, a.fillVisibleDetail()

	case detailMsg:
		a.detailBusy = false
		a.detailTried[detailKey(msg.screen, msg.name)] = true
		if msg.err != nil {
			// A row the domain will not describe is not worth a dialog: the
			// list it came from is still true, and the reason belongs in the
			// status line where the next read can replace it.
			a.setStatus(ui.StatusWarn, runner.FirstLine(msg.err.Error()))
			return a, a.fillVisibleDetail()
		}
		switch msg.screen {
		case directory.ScreenUsers:
			a.userDetail[msg.name] = msg.user
		case directory.ScreenComputers:
			a.computerDetail[msg.name] = msg.computer
		case directory.ScreenGroups:
			a.groupMembers[msg.name] = msg.members
		}
		a.applyFilter()
		return a, a.fillVisibleDetail()

	case ranMsg:
		a.busy = false
		if msg.err != nil {
			a.setStatus(ui.StatusError, runner.FirstLine(msg.err.Error()))
			return a, a.load()
		}
		summary := strings.TrimSpace(msg.output)
		if summary == "" {
			summary = "done"
		}
		a.setStatusf(ui.StatusOK, "%s: %s", msg.cmd.Description,
			runner.FirstLine(summary))
		// Re-read after every change: the domain is the source of truth, not
		// what the tool assumed would happen.
		a.loading = true
		return a, a.load()

	case tea.KeyMsg:
		return a.handleKey(msg)
	}

	// Anything else (cursor blink, …) only concerns an open text input.
	if a.mode == modeInput {
		cmd, _ := a.input.Update(msg)
		return a, cmd
	}
	return a, nil
}

// handleKey routes a key press to the open dialog or to the screen.
func (a *app) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c always quits, even mid-dialog.
	if msg.Type == tea.KeyCtrlC {
		return a, tea.Quit
	}
	if a.busy {
		// A command is running: swallow input rather than queueing surprises.
		return a, nil
	}

	switch a.mode {
	case modeConfirm:
		return a.handleConfirm(msg)
	case modeInput:
		return a.handleInput(msg)
	case modeHelp:
		a.mode = modeBrowse
		return a, nil
	case modeDetail:
		return a.handleDetail(msg)
	default:
		return a.handleBrowse(msg)
	}
}

// handleConfirm resolves the confirm dialog. This is the only path to a change.
func (a *app) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a.confirm.Update(msg)
	if !a.confirm.Done {
		return a, nil
	}
	a.mode = modeBrowse
	confirmed := a.confirm.Confirmed
	cmd, ok := a.confirm.Payload.(runner.Command)
	a.confirm = ui.Confirm{}
	if !confirmed || !ok {
		a.setStatus(ui.StatusInfo, "cancelled")
		return a, nil
	}
	a.busy = true
	a.setStatusf(ui.StatusInfo, "running %s…", a.backend.Preview(cmd))
	return a, a.run(cmd)
}

// handleInput resolves a prompt and, when it was accepted, opens the confirm
// dialog for the action that asked for it.
func (a *app) handleInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd, _ := a.input.Update(msg)
	if !a.input.Done {
		if a.pending.Action == "" {
			// The filter prompt narrows as the user types.
			a.filter = a.input.Value()
			a.applyFilter()
		}
		return a, cmd
	}
	value := a.input.Value()
	accepted := a.input.Accepted
	spec := a.pending
	target := a.pendingTarget
	a.pending, a.pendingTarget = directory.ActionSpec{}, directory.Intent{}
	a.mode = modeBrowse

	if spec.Action == "" {
		// The filter prompt: an accepted empty value clears it, an escape
		// restores nothing, which is the same thing.
		if accepted {
			a.filter = value
		} else {
			a.filter = ""
		}
		a.applyFilter()
		return a, nil
	}
	if !accepted {
		a.setStatus(ui.StatusInfo, "cancelled")
		return a, nil
	}
	target.Value = value
	return a, a.openConfirm(spec, target)
}

// handleDetail scrolls or closes the detail screen.
func (a *app) handleDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "enter":
		a.mode = modeBrowse
		a.detailOffset = 0
	case "j", "down":
		a.detailOffset++
	case "k", "up":
		a.detailOffset = max(a.detailOffset-1, 0)
	case "g", "home":
		a.detailOffset = 0
	}
	return a, nil
}

// handleBrowse handles a table screen.
func (a *app) handleBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// An action key applies to the current screen and always opens a prompt
	// or a confirm dialog first, so the action table is the single source of
	// truth for what each key does where.
	if spec, ok := directory.ActionFor(a.screen, key); ok {
		return a, a.startAction(spec)
	}

	switch key {
	case "q", "esc":
		return a, tea.Quit
	case "?":
		a.mode = modeHelp
	case "tab", "l", "right":
		a.setScreen((a.screen + 1) % directory.ScreenCount)
		return a, a.fillVisibleDetail()
	case "shift+tab", "h", "left":
		a.setScreen((a.screen + directory.ScreenCount - 1) % directory.ScreenCount)
		return a, a.fillVisibleDetail()
	case "1", "2", "3", "4", "5", "6":
		a.setScreen(directory.Screen(key[0] - '1'))
		return a, a.fillVisibleDetail()
	case "enter":
		return a, a.openDetail()
	case "j", "down":
		a.moveCursor(1)
		return a, a.fillVisibleDetail()
	case "k", "up":
		a.moveCursor(-1)
		return a, a.fillVisibleDetail()
	case "g", "home":
		a.cursor[a.screen], a.offset[a.screen] = 0, 0
		return a, a.fillVisibleDetail()
	case "G", "end":
		a.cursor[a.screen] = max(a.rowCount()-1, 0)
		a.clampCursor()
		return a, a.fillVisibleDetail()
	case "pgdown", "ctrl+f":
		a.moveCursor(a.tableHeight())
		return a, a.fillVisibleDetail()
	case "pgup", "ctrl+b":
		a.moveCursor(-a.tableHeight())
		return a, a.fillVisibleDetail()
	case "/":
		a.input = ui.NewInput("Filter", "name…", a.filter)
		a.input.Help = "Empty clears the filter. It applies to the current screen."
		a.mode = modeInput
	case "r", "ctrl+r":
		a.loading = true
		return a, a.load()
	}
	return a, nil
}

// detailKey names one row's detail, so a failed read on one screen does not
// suppress the read of a same-named row on another.
func detailKey(screen directory.Screen, name string) string {
	return screen.Title() + "/" + name
}

// fillVisibleDetail reads the detail of the first row on screen that has none.
//
// The lists are cheap and the detail is not: one samba-tool process per
// account. So the tool reads what the reader is actually looking at, one row
// at a time, chaining from each answer to the next — a viewport's worth rather
// than a domain's worth, and never more than one process at once. Scrolling
// pulls the rest in behind the cursor.
func (a *app) fillVisibleDetail() tea.Cmd {
	if a.detailBusy {
		return nil
	}
	screen := a.screen
	if screen != directory.ScreenUsers && screen != directory.ScreenGroups &&
		screen != directory.ScreenComputers {
		return nil
	}
	first := a.offset[screen]
	last := min(first+a.tableHeight(), a.rowCount())
	for i := first; i < last; i++ {
		name, ok := a.nameAt(screen, i)
		if !ok || a.detailTried[detailKey(screen, name)] || a.hasDetail(screen, name) {
			continue
		}
		a.detailBusy = true
		return a.readDetail(screen, name)
	}
	return nil
}

// hasDetail reports that a row has already been read.
func (a *app) hasDetail(screen directory.Screen, name string) bool {
	switch screen {
	case directory.ScreenUsers:
		_, ok := a.userDetail[name]
		return ok
	case directory.ScreenComputers:
		_, ok := a.computerDetail[name]
		return ok
	case directory.ScreenGroups:
		_, ok := a.groupMembers[name]
		return ok
	}
	return true
}

// nameAt is the name of a row by index on a given screen.
func (a *app) nameAt(screen directory.Screen, index int) (string, bool) {
	switch screen {
	case directory.ScreenUsers:
		if index < len(a.userRows) {
			return a.userRows[index].Name, true
		}
	case directory.ScreenGroups:
		if index < len(a.groupRows) {
			return a.groupRows[index].Name, true
		}
	case directory.ScreenComputers:
		if index < len(a.computerRows) {
			return a.computerRows[index].Name, true
		}
	}
	return "", false
}

// setScreen moves to another tab, keeping the filter, which is what a reader
// following one name across screens wants.
func (a *app) setScreen(screen directory.Screen) {
	a.screen = screen
	a.clampCursor()
}

// openDetail opens the detail screen for the selected row, reading what it
// needs if that has not been read yet.
func (a *app) openDetail() tea.Cmd {
	name, ok := a.selectedName()
	if !ok {
		return nil
	}
	a.mode = modeDetail
	a.detailOffset = 0
	switch a.screen {
	case directory.ScreenUsers:
		if _, read := a.userDetail[name]; !read {
			return a.readDetail(a.screen, name)
		}
	case directory.ScreenComputers:
		if _, read := a.computerDetail[name]; !read {
			return a.readDetail(a.screen, name)
		}
	case directory.ScreenGroups:
		if _, read := a.groupMembers[name]; !read {
			return a.readDetail(a.screen, name)
		}
	}
	return nil
}

// startAction begins an action: either a prompt for what it still needs, or
// the confirm dialog directly.
func (a *app) startAction(spec directory.ActionSpec) tea.Cmd {
	var intent directory.Intent
	intent.Action = spec.Action

	if spec.NeedsSelection {
		name, ok := a.selectedName()
		if !ok {
			a.setStatus(ui.StatusWarn, "nothing selected")
			return nil
		}
		intent.Target = name
		if a.screen == directory.ScreenDNS {
			record, ok := a.selectedRecord()
			if !ok {
				a.setStatus(ui.StatusWarn, "nothing selected")
				return nil
			}
			intent.Target, intent.Type, intent.Data = record.Node, record.Type, record.Data
			if !directory.KnownRecordType(record.Type) {
				a.setStatusf(ui.StatusWarn,
					"samba-tool does not delete a %s record from here", record.Type)
				return nil
			}
		}
	}

	if spec.Needs == directory.PromptNone {
		return a.openConfirm(spec, intent)
	}

	title := spec.PromptTitle
	if spec.NeedsSelection && intent.Target != "" {
		title += " — " + intent.Target
	}
	a.input = ui.NewInput(title, promptPlaceholder(spec), "")
	a.input.Help = spec.PromptHelp
	a.pending = spec
	a.pendingTarget = intent
	a.mode = modeInput
	return nil
}

// promptPlaceholder is the greyed-out example inside a prompt.
func promptPlaceholder(spec directory.ActionSpec) string {
	switch spec.Needs {
	case directory.PromptName:
		return "name…"
	case directory.PromptMember:
		return "account…"
	case directory.PromptDays:
		return "90"
	case directory.PromptRecord:
		return "ws03 A 10.10.0.23"
	}
	return ""
}

// openConfirm builds the command and opens the dialog that shows it. Nothing
// in this tool runs a command that did not come through here.
func (a *app) openConfirm(spec directory.ActionSpec, intent directory.Intent) tea.Cmd {
	intent.Zone = a.model.Zone.Name
	cmd, err := a.backend.Build(spec, intent)
	if err != nil {
		a.setStatus(ui.StatusError, err.Error())
		return nil
	}
	a.mode = modeConfirm
	a.confirm = ui.Confirm{
		Title:   cmd.Description,
		Body:    spec.Body,
		Command: a.backend.Preview(cmd),
		Danger:  cmd.Destructive,
		Payload: cmd,
	}
	return nil
}

// applyFilter recomputes the visible rows on every screen, folding in whatever
// detail has been read since the last time.
func (a *app) applyFilter() {
	needle := strings.ToLower(a.filter)
	keep := func(fields ...string) bool {
		if needle == "" {
			return true
		}
		for _, field := range fields {
			if strings.Contains(strings.ToLower(field), needle) {
				return true
			}
		}
		return false
	}

	a.factRows = a.factRows[:0]
	for _, row := range a.domainFacts() {
		if keep(row.label, row.value) {
			a.factRows = append(a.factRows, row)
		}
	}

	a.userRows = nil
	for _, user := range a.model.Users {
		if detail, ok := a.userDetail[user.Name]; ok {
			user = detail
		}
		if keep(user.Name, user.DisplayName, user.Mail, user.Description) {
			a.userRows = append(a.userRows, user)
		}
	}

	a.groupRows = nil
	for _, group := range a.model.Groups {
		if members, ok := a.groupMembers[group.Name]; ok {
			group.Members, group.MembersRead = members, true
		}
		if keep(group.Name, group.Description) {
			a.groupRows = append(a.groupRows, group)
		}
	}

	a.computerRows = nil
	for _, computer := range a.model.Computers {
		if detail, ok := a.computerDetail[computer.Name]; ok {
			computer = detail
		}
		if keep(computer.Name, computer.DNSName, computer.OS) {
			a.computerRows = append(a.computerRows, computer)
		}
	}

	a.recordRows = nil
	for _, record := range a.model.Zone.Records {
		if keep(record.Node, record.Type, record.Data) {
			a.recordRows = append(a.recordRows, record)
		}
	}

	a.partRows = nil
	for _, partition := range a.model.Repl.Partitions {
		if keep(partition.DN, partition.Neighbor, partition.Direction) {
			a.partRows = append(a.partRows, partition)
		}
	}

	a.clampCursor()
}

// rowCount is how many rows the current screen has.
func (a *app) rowCount() int {
	switch a.screen {
	case directory.ScreenUsers:
		return len(a.userRows)
	case directory.ScreenGroups:
		return len(a.groupRows)
	case directory.ScreenComputers:
		return len(a.computerRows)
	case directory.ScreenDNS:
		return len(a.recordRows)
	case directory.ScreenRepl:
		return len(a.partRows)
	default:
		return len(a.factRows)
	}
}

// selectedName returns the name of the highlighted row, when the screen has
// rows that are named things.
func (a *app) selectedName() (string, bool) {
	index := a.cursor[a.screen]
	if index < 0 || index >= a.rowCount() {
		return "", false
	}
	switch a.screen {
	case directory.ScreenUsers:
		return a.userRows[index].Name, true
	case directory.ScreenGroups:
		return a.groupRows[index].Name, true
	case directory.ScreenComputers:
		return a.computerRows[index].Name, true
	case directory.ScreenDNS:
		return a.recordRows[index].Node, true
	default:
		return "", false
	}
}

// selectedRecord returns the highlighted DNS record.
func (a *app) selectedRecord() (directory.Record, bool) {
	index := a.cursor[a.screen]
	if a.screen != directory.ScreenDNS || index < 0 || index >= len(a.recordRows) {
		return directory.Record{}, false
	}
	return a.recordRows[index], true
}

// moveCursor moves the selection and keeps the viewport in sync.
func (a *app) moveCursor(delta int) {
	a.cursor[a.screen] += delta
	a.clampCursor()
}

// clampCursor keeps the cursor and the scroll offset within range.
func (a *app) clampCursor() {
	count := a.rowCount()
	if count == 0 {
		a.cursor[a.screen], a.offset[a.screen] = 0, 0
		return
	}
	a.cursor[a.screen] = min(max(a.cursor[a.screen], 0), count-1)

	height := a.tableHeight()
	if a.cursor[a.screen] < a.offset[a.screen] {
		a.offset[a.screen] = a.cursor[a.screen]
	}
	if a.cursor[a.screen] >= a.offset[a.screen]+height {
		a.offset[a.screen] = a.cursor[a.screen] - height + 1
	}
	a.offset[a.screen] = max(min(a.offset[a.screen], max(count-height, 0)), 0)
}
