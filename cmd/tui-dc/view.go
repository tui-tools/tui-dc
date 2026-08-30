package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-kit/ui"
)

// Layout constants: the rows the table cannot use.
const (
	headerLines = 2
	tabLines    = 1
	footerLines = 2
	// minTableHeight keeps at least one visible row on a very short terminal.
	minTableHeight = 1
)

// tableHeight is the number of rows that fit on screen.
func (a *app) tableHeight() int {
	// header + tabs + table header + help bar + status line.
	return max(a.height-headerLines-tabLines-footerLines-2, minTableHeight)
}

// detailHeight is the number of detail lines that fit on screen.
func (a *app) detailHeight() int {
	return max(a.height-headerLines-footerLines-1, minTableHeight)
}

// View renders the whole screen.
func (a *app) View() string {
	switch a.mode {
	case modeConfirm:
		return a.confirm.View(a.theme, a.width, a.height)
	case modeInput:
		return a.input.View(a.theme, a.width, a.height)
	case modeHelp:
		return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center,
			ui.HelpScreen(a.theme, "tui-dc — keys", a.helpKeys(), a.width))
	case modeDetail:
		return a.detailView()
	default:
		return a.browseView()
	}
}

// browseView renders a screen: header, tab bar, table, help bar, status. Every
// tool in the family draws these same bands.
func (a *app) browseView() string {
	var body string
	switch {
	case a.loading && a.rowCount() == 0:
		body = ui.EmptyState(a.theme, "reading the domain…", a.width, a.tableHeight()+1)
	case a.rowCount() == 0:
		body = ui.EmptyState(a.theme, a.emptyMessage(), a.width, a.tableHeight()+1)
	default:
		body = a.table()
	}

	help := ui.HelpBar(a.theme, a.shortHelpKeys(), a.width)
	status := ui.StatusLine(a.theme, a.statusKind, a.status, a.defaultStatus(), a.width)
	return strings.Join([]string{a.headerView(), a.tabsView(), body, help, status}, "\n")
}

// emptyMessage is what a screen with no rows says, which is different on each
// and different again depending on why there is nothing.
func (a *app) emptyMessage() string {
	if a.loadFailed {
		return "could not read the domain — see the message below"
	}
	if !a.model.Installed {
		return "samba-tool is not installed here"
	}
	if a.filter != "" {
		return "nothing matches " + strconv.Quote(a.filter)
	}
	switch a.screen {
	case directory.ScreenUsers:
		return "no accounts — `samba-tool user list` returned nothing"
	case directory.ScreenGroups:
		return "no groups — `samba-tool group list` returned nothing"
	case directory.ScreenComputers:
		return "no machine accounts"
	case directory.ScreenDNS:
		if !a.model.Zone.Read {
			return "the zone could not be queried — see the domain screen"
		}
		return "the zone is empty"
	case directory.ScreenRepl:
		if !a.model.Repl.Read {
			return "replication could not be read — see the domain screen"
		}
		return "no replication partners: this is the only controller"
	default:
		return "nothing to show"
	}
}

// headerView renders the facts at the top of every screen.
func (a *app) headerView() string {
	counts := a.model.Count()
	facts := []ui.Fact{
		{Label: "users", Value: strconv.Itoa(counts.Users)},
		{Label: "groups", Value: strconv.Itoa(counts.Groups)},
		{Label: "computers", Value: strconv.Itoa(counts.Computers)},
	}

	// Whether replication is healthy is the one fact worth carrying onto every
	// screen: it is the thing that is wrong when everything else looks right.
	if a.model.Repl.Read {
		value, style := a.replFact(counts)
		facts = append(facts, ui.Fact{Label: "replication", Value: value, Style: style})
	}
	if a.backendCompat.Backend != "" {
		facts = append(facts, ui.CompatFact(a.theme, a.backendCompat))
	}

	subtitle := a.subtitle()
	return ui.Header{Title: "tui-dc", Subtitle: subtitle, Facts: facts}.
		Render(a.theme, a.width)
}

// subtitle names the domain, or says why it cannot.
func (a *app) subtitle() string {
	parts := []string{}
	switch {
	case a.model.Domain.Realm != "":
		parts = append(parts, a.model.Domain.Realm)
	case !a.model.Installed:
		parts = append(parts, "no samba-tool")
	default:
		parts = append(parts, "domain unknown")
	}
	if !a.model.Reachable && a.model.Installed {
		parts = append(parts, "no controller answered")
	} else if a.model.Domain.DCNetBIOS != "" {
		parts = append(parts, a.model.Domain.DCNetBIOS)
	}
	parts = append(parts, a.backend.Describe())
	if a.filter != "" {
		parts = append(parts, "filter: "+a.filter)
	}
	return strings.Join(parts, "  ·  ")
}

// replFact is the replication word in the header, coloured by what it says.
func (a *app) replFact(counts directory.Counts) (string, *lipgloss.Style) {
	switch {
	case counts.Partitions == 0:
		style := a.theme.Muted
		return "single DC", &style
	case counts.Failing > 0:
		style := a.theme.Danger
		return fmt.Sprintf("%d of %d failing", counts.Failing, counts.Partitions), &style
	default:
		style := a.theme.OK
		return "ok", &style
	}
}

// tabsView renders the six screens as one row, with the current one accented.
func (a *app) tabsView() string {
	var parts []string
	for s := directory.Screen(0); s < directory.ScreenCount; s++ {
		label := " " + s.Title() + " "
		if s == a.screen {
			parts = append(parts, a.theme.Accent.Render(label))
			continue
		}
		parts = append(parts, a.theme.Muted.Render(label))
	}
	return ui.Truncate(strings.Join(parts, a.theme.Muted.Render("│")), a.width)
}

// defaultStatus is the hint shown when there is no message to report.
func (a *app) defaultStatus() string {
	noun := map[directory.Screen]string{
		directory.ScreenDomain:    "facts",
		directory.ScreenUsers:     "accounts",
		directory.ScreenGroups:    "groups",
		directory.ScreenComputers: "computers",
		directory.ScreenDNS:       "records",
		directory.ScreenRepl:      "partitions",
	}[a.screen]
	return strconv.Itoa(a.rowCount()) + " " + noun + "  ·  tab to move, ? for help"
}

// table renders the current screen's rows.
func (a *app) table() string {
	columns, rows, styles := a.tableData()
	return ui.Table{
		Columns:  columns,
		Rows:     rows,
		Styles:   styles,
		Selected: a.cursor[a.screen],
		Offset:   a.offset[a.screen],
		Height:   a.tableHeight(),
	}.Render(a.theme, a.width)
}

// tableData builds the columns, cells and row styles of the current screen.
// Every screen drops its widest columns first on a narrow terminal, which is
// how the family keeps working at 40 columns.
func (a *app) tableData() ([]ui.Column, [][]string, []*lipgloss.Style) {
	switch a.screen {
	case directory.ScreenUsers:
		return a.usersTable()
	case directory.ScreenGroups:
		return a.groupsTable()
	case directory.ScreenComputers:
		return a.computersTable()
	case directory.ScreenDNS:
		return a.dnsTable()
	case directory.ScreenRepl:
		return a.replTable()
	default:
		return a.domainTable()
	}
}

// domainFacts is the domain screen: what the controller and this host said,
// in the order somebody debugging reads them.
func (a *app) domainFacts() []factRow {
	d := a.model.Domain
	unknown := func(value string) factRow {
		return factRow{value: firstNonEmpty(value, "unknown"), note: value == ""}
	}
	rows := []factRow{
		{label: "realm", value: firstNonEmpty(d.Realm, "unknown"), note: d.Realm == ""},
		{label: "forest", value: firstNonEmpty(d.Forest, d.Realm, "unknown"),
			note: d.Forest == "" && d.Realm == ""},
		{label: "netbios domain", value: firstNonEmpty(d.NetBIOS, "unknown"),
			note: d.NetBIOS == ""},
		{label: "this host's role",
			value: firstNonEmpty(d.ServerRole, "unknown"), note: !d.IsDC()},
		{label: "answering DC",
			value: firstNonEmpty(d.DCName, "none answered"), note: d.DCName == ""},
		{label: "server site", value: firstNonEmpty(d.ServerSite, "unknown"),
			note: d.ServerSite == ""},
	}
	if d.ClientSite != "" && d.ClientSite != d.ServerSite {
		rows = append(rows, factRow{label: "client site", value: d.ClientSite, note: true})
	}
	rows = append(rows,
		factRow{label: "forest function level", value: unknown(d.ForestLevel).value,
			note: d.ForestLevel == ""},
		factRow{label: "domain function level", value: unknown(d.DomainLevel).value,
			note: d.DomainLevel == ""},
		factRow{label: "lowest DC level", value: unknown(d.LowestLevel).value,
			note: d.LowestLevel == ""},
		factRow{label: "DNS backend", value: firstNonEmpty(d.DNSBackend, "unknown"),
			note: d.DNSBackend == ""},
		factRow{label: "samba-tool", value: firstNonEmpty(a.model.Version, "not installed"),
			note: !a.model.Installed},
	)

	// The notes are part of the domain screen rather than hidden behind a key:
	// a read that half failed is the most important thing on it.
	for _, note := range a.model.Notes {
		rows = append(rows, factRow{label: "note", value: note, note: true})
	}
	return rows
}

// domainTable renders the domain screen.
func (a *app) domainTable() ([]ui.Column, [][]string, []*lipgloss.Style) {
	columns := []ui.Column{
		{Title: "FACT", Width: 24},
		{Title: "VALUE", Width: 40, Flex: true},
	}
	rows := make([][]string, 0, len(a.factRows))
	styles := make([]*lipgloss.Style, 0, len(a.factRows))
	for _, row := range a.factRows {
		rows = append(rows, []string{row.label, row.value})
		styles = append(styles, a.noteStyle(row.note))
	}
	return columns, rows, styles
}

// usersTable renders the accounts screen. State and display name are only
// known for an account whose detail has been read, and a row that does not
// know says so rather than guessing "enabled".
func (a *app) usersTable() ([]ui.Column, [][]string, []*lipgloss.Style) {
	columns := []ui.Column{
		{Title: "ACCOUNT", Width: 20, Flex: true},
		{Title: "STATE", Width: 9},
	}
	wide := a.width >= 70
	if wide {
		columns = append(columns, ui.Column{Title: "NAME", Width: 22})
	}
	veryWide := a.width >= 100
	if veryWide {
		columns = append(columns, ui.Column{Title: "MAIL", Width: 24})
	}

	rows := make([][]string, 0, len(a.userRows))
	styles := make([]*lipgloss.Style, 0, len(a.userRows))
	for _, user := range a.userRows {
		state := "?"
		if _, read := a.userDetail[user.Name]; read {
			state = user.State()
		}
		row := []string{user.Name, state}
		if wide {
			row = append(row, user.DisplayName)
		}
		if veryWide {
			row = append(row, user.Mail)
		}
		rows = append(rows, row)
		styles = append(styles, a.userStyle(user, state))
	}
	return columns, rows, styles
}

// groupsTable renders the groups screen.
func (a *app) groupsTable() ([]ui.Column, [][]string, []*lipgloss.Style) {
	columns := []ui.Column{
		{Title: "GROUP", Width: 28, Flex: true},
		{Title: "MEMBERS", Width: 9},
	}
	wide := a.width >= 70
	if wide {
		columns = append(columns, ui.Column{Title: "DESCRIPTION", Width: 34})
	}

	rows := make([][]string, 0, len(a.groupRows))
	styles := make([]*lipgloss.Style, 0, len(a.groupRows))
	for _, group := range a.groupRows {
		members := "?"
		if group.MembersRead {
			members = strconv.Itoa(len(group.Members))
		}
		row := []string{group.Name, members}
		if wide {
			row = append(row, group.Description)
		}
		rows = append(rows, row)
		styles = append(styles, a.noteStyle(false))
	}
	return columns, rows, styles
}

// computersTable renders the machine accounts screen.
func (a *app) computersTable() ([]ui.Column, [][]string, []*lipgloss.Style) {
	columns := []ui.Column{
		{Title: "COMPUTER", Width: 18, Flex: true},
		{Title: "STATE", Width: 9},
	}
	wide := a.width >= 70
	if wide {
		columns = append(columns, ui.Column{Title: "DNS NAME", Width: 26})
	}
	veryWide := a.width >= 100
	if veryWide {
		columns = append(columns, ui.Column{Title: "OS", Width: 20})
	}

	rows := make([][]string, 0, len(a.computerRows))
	styles := make([]*lipgloss.Style, 0, len(a.computerRows))
	for _, computer := range a.computerRows {
		state := "?"
		if _, read := a.computerDetail[computer.Name]; read {
			state = "enabled"
			if computer.Disabled {
				state = "disabled"
			}
		}
		row := []string{computer.Name, state}
		if wide {
			row = append(row, computer.DNSName)
		}
		if veryWide {
			row = append(row, computer.OS)
		}
		rows = append(rows, row)
		styles = append(styles, a.noteStyle(computer.Disabled))
	}
	return columns, rows, styles
}

// dnsTable renders the domain zone.
func (a *app) dnsTable() ([]ui.Column, [][]string, []*lipgloss.Style) {
	columns := []ui.Column{
		{Title: "NAME", Width: 18},
		{Title: "TYPE", Width: 6},
		{Title: "DATA", Width: 30, Flex: true},
	}
	wide := a.width >= 70
	if wide {
		columns = append(columns, ui.Column{Title: "TTL", Width: 7})
	}

	rows := make([][]string, 0, len(a.recordRows))
	styles := make([]*lipgloss.Style, 0, len(a.recordRows))
	for _, record := range a.recordRows {
		row := []string{record.Node, record.Type, record.Data}
		if wide {
			ttl := "-"
			if record.TTL > 0 {
				ttl = strconv.Itoa(record.TTL)
			}
			row = append(row, ttl)
		}
		rows = append(rows, row)
		// A record type this tool cannot delete is dimmed, so the reader can
		// see before pressing d which rows the key does not apply to.
		styles = append(styles, a.noteStyle(!directory.KnownRecordType(record.Type)))
	}
	return columns, rows, styles
}

// replTable renders one row per naming context per direction.
func (a *app) replTable() ([]ui.Column, [][]string, []*lipgloss.Style) {
	columns := []ui.Column{
		{Title: "PARTITION", Width: 30, Flex: true},
		{Title: "DIR", Width: 9},
		{Title: "STATE", Width: 12},
	}
	wide := a.width >= 80
	if wide {
		columns = append(columns, ui.Column{Title: "NEIGHBOR", Width: 26})
	}

	rows := make([][]string, 0, len(a.partRows))
	styles := make([]*lipgloss.Style, 0, len(a.partRows))
	for _, partition := range a.partRows {
		state := "ok"
		if !partition.OK {
			state = fmt.Sprintf("%d failures", partition.Failures)
		}
		row := []string{partition.DN, partition.Direction, state}
		if wide {
			row = append(row, partition.Neighbor)
		}
		rows = append(rows, row)
		style := a.theme.Row
		if !partition.OK {
			style = a.theme.Row.Foreground(a.theme.Danger.GetForeground())
		}
		styles = append(styles, &style)
	}
	return columns, rows, styles
}

// userStyle colours an account row, so the eye finds what matters without
// reading every cell.
func (a *app) userStyle(user directory.User, state string) *lipgloss.Style {
	style := a.theme.Row
	switch {
	case state == "?":
		style = a.theme.Row.Foreground(a.theme.Muted.GetForeground())
	case user.Disabled, user.Locked:
		style = a.theme.Row.Foreground(a.theme.Warn.GetForeground())
	}
	return &style
}

// noteStyle dims a row that is a caveat rather than a fact.
func (a *app) noteStyle(note bool) *lipgloss.Style {
	style := a.theme.Row
	if note {
		style = a.theme.Row.Foreground(a.theme.Warn.GetForeground())
	}
	return &style
}

// detailView renders the detail screen for the selected row.
func (a *app) detailView() string {
	lines := a.detailLines()
	height := a.detailHeight()
	offset := min(a.detailOffset, max(len(lines)-height, 0))
	if offset < len(lines) {
		lines = lines[offset:]
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	body := make([]string, 0, len(lines))
	for _, line := range lines {
		body = append(body, a.theme.Row.Render(ui.Truncate(line, a.width)))
	}

	help := ui.HelpBar(a.theme, []ui.KeyHint{
		{Key: "↑/↓", Desc: "scroll"},
		{Key: "esc", Desc: "back"},
		{Key: "q", Desc: "back"},
	}, a.width)
	status := ui.StatusLine(a.theme, a.statusKind, a.status,
		"read with samba-tool show", a.width)
	return strings.Join(append([]string{a.headerView()},
		append(body, help, status)...), "\n")
}

// detailLines builds the detail screen's text for whichever row is selected.
func (a *app) detailLines() []string {
	name, ok := a.selectedName()
	if !ok {
		return []string{"nothing selected"}
	}
	switch a.screen {
	case directory.ScreenUsers:
		return a.userDetailLines(name)
	case directory.ScreenGroups:
		return a.groupDetailLines(name)
	case directory.ScreenComputers:
		return a.computerDetailLines(name)
	case directory.ScreenDNS:
		record, _ := a.selectedRecord()
		return []string{
			"record  " + record.Node + "  " + record.Type,
			"data    " + record.Data,
			"ttl     " + strconv.Itoa(record.TTL),
			"zone    " + a.model.Zone.Name,
			"",
			"Read with `samba-tool dns query`. Deleting a record the domain's own",
			"members resolve through can make the domain unreachable for them.",
		}
	case directory.ScreenRepl:
		return a.replDetailLines()
	default:
		return []string{"the domain screen has no detail: it is all detail"}
	}
}

// userDetailLines is one account, as far as it has been read.
func (a *app) userDetailLines(name string) []string {
	user, read := a.userDetail[name]
	if !read {
		return []string{"reading `samba-tool user show " + name + "`…"}
	}
	lines := []string{
		"account       " + user.Name,
		"display name  " + fallback(user.DisplayName),
		"mail          " + fallback(user.Mail),
		"description   " + fallback(user.Description),
		"state         " + user.State(),
		"expires       " + fallback(user.Expires),
		"password set  " + fallback(user.PasswordLast),
		"last logon    " + fallback(user.LastLogon),
		"dn            " + fallback(user.DN),
		"",
		"member of",
	}
	if len(user.Groups) == 0 {
		lines = append(lines, "  (only the primary group)")
	}
	for _, group := range user.Groups {
		lines = append(lines, "  "+group)
	}
	if user.Detail != "" {
		lines = append(lines, "", "samba-tool user show "+name, "")
		lines = append(lines, strings.Split(user.Detail, "\n")...)
	}
	return lines
}

// groupDetailLines is one group and its membership.
func (a *app) groupDetailLines(name string) []string {
	members, read := a.groupMembers[name]
	if !read {
		return []string{"reading `samba-tool group listmembers " + name + "`…"}
	}
	lines := []string{
		"group    " + name,
		fmt.Sprintf("members  %d", len(members)),
		"",
	}
	if len(members) == 0 {
		return append(lines, "  (empty)")
	}
	for _, member := range members {
		lines = append(lines, "  "+member)
	}
	return lines
}

// computerDetailLines is one machine account.
func (a *app) computerDetailLines(name string) []string {
	computer, read := a.computerDetail[name]
	if !read {
		return []string{"reading `samba-tool computer show " + name + "`…"}
	}
	state := "enabled"
	if computer.Disabled {
		state = "disabled"
	}
	lines := []string{
		"computer    " + computer.Name,
		"state       " + state,
		"dns name    " + fallback(computer.DNSName),
		"os          " + strings.TrimSpace(computer.OS+" "+computer.OSVersion),
		"description " + fallback(computer.Description),
		"dn          " + fallback(computer.DN),
	}
	if computer.Detail != "" {
		lines = append(lines, "", "samba-tool computer show "+name, "")
		lines = append(lines, strings.Split(computer.Detail, "\n")...)
	}
	return lines
}

// replDetailLines is the whole replication answer, which is short enough to
// read at once and is the thing an operator wants in full.
func (a *app) replDetailLines() []string {
	repl := a.model.Repl
	lines := []string{
		"controller  " + fallback(strings.TrimSpace(repl.Site+`\`+repl.Name)),
		"guid        " + fallback(repl.GUID),
		"",
	}
	if !repl.Read {
		return append(lines, "replication could not be read:", "  "+fallback(repl.Detail))
	}
	index := a.cursor[a.screen]
	if index < 0 || index >= len(a.partRows) {
		return append(lines, "nothing selected")
	}
	partition := a.partRows[index]
	state := "the last attempt succeeded"
	if !partition.OK {
		state = partition.Detail
	}
	return append(lines,
		"partition   "+partition.DN,
		"direction   "+partition.Direction,
		"neighbor    "+fallback(partition.Neighbor),
		"transport   "+fallback(partition.Transport),
		"last try    "+fallback(partition.LastAttempt),
		"last ok     "+fallback(partition.LastSuccess),
		fmt.Sprintf("failures    %d", partition.Failures),
		"",
		state,
	)
}

// shortHelpKeys is the single-line hint bar, which changes with the screen
// because the action keys do.
func (a *app) shortHelpKeys() []ui.KeyHint {
	hints := []ui.KeyHint{{Key: "tab", Desc: "screen"}}
	if a.screen != directory.ScreenDomain && a.screen != directory.ScreenRepl {
		hints = append(hints, ui.KeyHint{Key: "enter", Desc: "detail"})
	}
	for _, spec := range directory.ActionsFor(a.screen) {
		hints = append(hints, ui.KeyHint{
			Key: spec.Key, Desc: strings.ToLower(shortLabel(spec.Label))})
	}
	return append(hints,
		ui.KeyHint{Key: "/", Desc: "filter"},
		ui.KeyHint{Key: "r", Desc: "re-read"},
		ui.KeyHint{Key: "?", Desc: "help"},
		ui.KeyHint{Key: "q", Desc: "quit"},
	)
}

// shortLabel is a label's first word, which is what fits in the hint bar.
func shortLabel(label string) string {
	if idx := strings.Index(label, " "); idx > 0 {
		return label[:idx]
	}
	return label
}

// helpKeys is the full key list. The action rows are generated from the action
// table, so a new action cannot be missing from the help.
func (a *app) helpKeys() []ui.KeyHint {
	hints := []ui.KeyHint{
		{Key: "tab / shift+tab", Desc: "next / previous screen"},
		{Key: "1 … 6", Desc: "go straight to a screen"},
		{Key: "↑/k, ↓/j", Desc: "move the selection"},
		{Key: "g / G", Desc: "first / last row"},
		{Key: "pgup/pgdn", Desc: "scroll a page"},
		{Key: "enter", Desc: "open the selected row (reads it if it has not been read)"},
		{Key: "/", Desc: "filter the current screen (esc clears)"},
		{Key: "", Desc: ""},
	}
	for s := directory.Screen(0); s < directory.ScreenCount; s++ {
		specs := directory.ActionsFor(s)
		if len(specs) == 0 {
			continue
		}
		hints = append(hints, ui.KeyHint{Key: "", Desc: s.Title() + ":"})
		for _, spec := range specs {
			hints = append(hints, ui.KeyHint{
				Key: spec.Key, Desc: strings.ToLower(spec.Label)})
		}
	}
	return append(hints,
		ui.KeyHint{Key: "", Desc: ""},
		ui.KeyHint{Key: "r", Desc: "re-read the domain"},
		ui.KeyHint{Key: "?", Desc: "this help"},
		ui.KeyHint{Key: "q", Desc: "quit"},
		ui.KeyHint{Key: "", Desc: ""},
		ui.KeyHint{Key: "note",
			Desc: "every change is one samba-tool command, previewed and confirmed first"},
		ui.KeyHint{Key: "",
			Desc: "no password is ever an argument: samba-tool is asked for a random one"},
	)
}

// fallback renders an empty value as a dash rather than as nothing, so a
// column that is blank is visibly blank.
func fallback(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// firstNonEmpty returns the first value that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
