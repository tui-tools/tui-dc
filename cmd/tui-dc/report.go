package main

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/tui-tools/tui-dc/internal/samba"
	"github.com/tui-tools/tui-kit/config"
	"github.com/tui-tools/tui-kit/report"
	"github.com/tui-tools/tui-kit/theme"
)

// runReport prints the block a bug report needs and exits. Every tool in the
// family has this function, and it is worth keeping it recognisable.
//
// Everything generic — the kit version, the distribution, the kernel, the
// terminal, where the binary came from — is collected by the kit, so the whole
// family answers --report in the same shape. What this tool adds is the part
// only it knows: the samba-tool version the compat probe read, and whether
// this host is configured as a domain controller at all, which is the first
// thing to know about a tui-dc bug and the last thing a reporter thinks to
// mention.
//
// Two rules make it worth copying. It reads nothing privileged, because a user
// who cannot escalate is the one who most needs to be able to file a usable
// bug — so the domain-controller question is answered from this host's own
// configuration file rather than by running samba-tool as root. And it runs
// before the backend is built, so a machine with no Samba at all still
// produces a report, with the failure as one of its lines.
func runReport(cfg config.Config, opts options, out io.Writer) error {
	palette, _ := theme.ResolvePalette()

	// The same probe the header uses. There is one version probe in a tool and
	// this is it — a report that probed separately could disagree with the
	// header the user is looking at.
	backendCompat := probeCompat(context.Background(), opts.demo)

	var backendError string
	if _, err := pickBackend(cfg, opts, backendCompat); err != nil {
		backendError = err.Error()
	}

	info := report.Info{
		Tool:           toolName,
		Version:        version,
		Backend:        backendName,
		BackendVersion: backendCompat.Version,
		BackendDetail:  backendCompat.Detail,
		Demo:           opts.demo,
		Sudo:           cfg.String(config.KeySudo, ""),
		Theme:          palette.Name,
	}

	info.Extra = append(info.Extra, report.Field{
		Key: "samba-tool", Value: sambaToolLine(backendCompat.Version),
	})
	info.Extra = append(info.Extra, report.Field{
		Key: "this host", Value: hostRole(),
	})
	info.Extra = append(info.Extra, report.Field{
		Key: "server", Value: cfg.String(keyServer, samba.DefaultServer),
	})

	if opts.demo {
		// The fake imitates samba-tool, and which backend it imitates decides
		// which command builders and which parsers the session exercised. A
		// fake is free to call itself "demo" — this one does — so the imitated
		// backend is named here rather than asked of the fake.
		info.Backend = "demo"
		info.Extra = append(info.Extra, report.Field{
			Key: "demo backend", Value: backendName,
		})
	}
	if backendError != "" {
		info.Extra = append(info.Extra, report.Field{
			Key: "backend error", Value: scrubHome(backendError),
		})
	}

	_, err := io.WriteString(out, report.Render(info))
	return err
}

// sambaToolLine says whether samba-tool is on this machine, which is a
// different fact from what version it is: a probe that found nothing and a
// probe that could not parse the output both leave the version empty.
func sambaToolLine(version string) string {
	if !samba.Available() {
		return "not installed"
	}
	if version == "" {
		return "installed, version unknown"
	}
	return "installed"
}

// smbConfPaths are where a Samba configuration lives. The list is short and
// fixed, because reading a path the user could point anywhere would be a way
// to make --report disclose a file it was never meant to read.
var smbConfPaths = []string{
	"/etc/samba/smb.conf",
	"/usr/local/samba/etc/smb.conf",
}

// serverRoleRe matches the one line of smb.conf --report is interested in.
var serverRoleRe = regexp.MustCompile(`(?im)^\s*server role\s*=\s*(.+)$`)

// hostRole answers the question every tui-dc bug starts with: is this machine
// a domain controller? It is read straight out of smb.conf, unprivileged and
// without running anything, so it works on the machine of a user who cannot
// escalate — which is the machine most likely to be filing the bug.
func hostRole() string {
	for _, path := range smbConfPaths {
		data, err := readSmallFile(path)
		if err != nil {
			continue
		}
		match := serverRoleRe.FindStringSubmatch(data)
		if match == nil {
			return "has smb.conf, no server role set"
		}
		role := strings.TrimSpace(match[1])
		if strings.Contains(strings.ToLower(role), "domain controller") {
			return "a domain controller (" + role + ")"
		}
		return "not a domain controller (server role = " + role + ")"
	}
	return "no smb.conf found"
}

// homePath matches a path under a home directory, which is the one thing this
// tool could otherwise put in the block that names its user: an error message
// from a runner that could not find a binary carries the PATH it searched.
var homePath = regexp.MustCompile(`(/home|/root)(/[^\s:]*)?`)

// scrubHome replaces such a path with the placeholder the kit uses for the
// same reason. The block is pasted into a public issue, so a value a tool hands
// to report.Extra is its own responsibility: the kit scrubs what it collected
// itself and cannot know what is inside a message a tool passes on.
func scrubHome(s string) string {
	return homePath.ReplaceAllString(s, "~elsewhere~")
}

// reportUsage is the flag's one-line help, kept here next to what it prints.
var reportUsage = fmt.Sprintf(
	"print the versions and machine facts a bug report needs, then exit "+
		"(no UI, no privileges, nothing about your domain: paste it into a %s issue)",
	toolName)
