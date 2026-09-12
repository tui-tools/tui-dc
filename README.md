<img src="assets/logo.png" alt="tui-tools" width="240">

> **Private and unreleased.** This repository is not public yet and nothing has
> been tagged. It has never been run against a real Samba domain controller —
> see [What is still missing](#what-is-still-missing) before trusting it with
> one.

# tui-dc

A Samba Active Directory domain, administered from the terminal.

It opens read-only. One pass over `samba-tool` gives you the domain's name and
functional levels, the role this host plays in it, the accounts, the groups,
the machine accounts, the domain's own DNS zone and the state of replication
per naming context — six screens, one key apart.

Every change is one `samba-tool` command, shown in full and confirmed before it
runs.

![The domain screen](docs/screenshots/tui-dc-domain.png)

![The accounts screen](docs/screenshots/tui-dc-users.png)

![The confirm dialog](docs/screenshots/tui-dc-suspend.png)

```sh
make demo     # a sample domain, on a machine with no Samba on it
```

On a machine that has `samba-tool` and no domain, the domain screen offers one:
`P` runs a preflight and then opens the provision wizard — realm, NetBIOS name,
DNS backend, optional forwarder, the address this controller serves — and ends
in the same previewed, confirmed command as every other change, gated by typing
the realm back. What follows is a short chain of previewed steps that ends with
a controller that is actually serving. Run `tui-dc --demo-fresh` to walk the
whole thing against a fake machine.

## No password ever reaches a command line

samba-tool says this itself, every time you give it one:

```
WARNING: Using passwords on command line is insecure.
```

It is right: a command line is visible in `ps` to every user on the machine. So
this tool never builds one. Creating an account and resetting a password both
run with `--random-password`, and what samba-tool printed is what you hand over.

Provisioning follows the same rule by omission: the wizard's command carries no
`--adminpass` at all. Left out, samba-tool generates a strong Administrator
password itself and prints `Admin password:` exactly once when provisioning
finishes — the result screen shows that line once, stores it nowhere, and the
password never exists in an argv, in this process, or anywhere but samba-tool's
own output. There is a test asserting that no action in the table can ever put
a password in an argv, and it is not there for decoration — it is the rule this
tool is built around and the first thing a new action would break.

## Provisioning a domain

When the read finds `samba-tool` but no domain — `smb.conf` does not say this
host is a domain controller — the domain screen says so and `P` starts.

### The preflight

The conditions that decide whether provisioning can work at all are checked
before the first question, because every one of them used to be found late: one
after the realm had been typed twice and the command confirmed, the rest
somewhere inside a provision that had already been building a directory for
minutes. A host where none of them holds sees no extra screen — the wizard opens
exactly as it did before.

Each is a fact on disk, and the screen says which fact it read. Nothing is
installed by this tool, ever: the conditions a person has to clear are stated
with the package that carries the missing piece **on this distribution**, and on
a distribution whose package names have not been verified the tool names the
path it looked at and says it does not know the package rather than guessing
one.

- **A distribution's own `/etc/samba/smb.conf`.** Fedora's and Ubuntu's `samba`
  package ships one with `security = user`, which `testparm` resolves to
  `server role = auto`, and provision refuses to start unless the resolved role
  is already the DC one. The tool offers the way out as a previewed command like
  any other change: `mv /etc/samba/smb.conf /etc/samba/smb.conf.orig`. Provision
  then writes its own — which is also why the confirm says plainly that a file
  server's configuration is what is being moved aside. If `smb.conf.orig`
  already exists the file is **not** moved: the tool says so and leaves that one
  to you, because one previewed command must never overwrite a saved
  configuration. Arch ships no `smb.conf` at all, and the condition does not
  appear there.
- **The AD provisioning data.** `samba-tool` and the AD schema come from
  different packages on two of the three distributions, and without the schema
  provision fails part of the way in on a missing `.ldf` file. A `stat` of
  `/usr/share/samba/setup/ad-schema/` answers it first.
- **What a provision needs beyond samba-tool and the schema** — the five pieces
  in the table below, as **one** condition with a list in it. Each was found by a
  provision that ran for minutes and then died, and each is checked without
  asking a package manager anything: a `.so` is in samba's module directory or it
  is not, `python3` can find a module or it cannot, `winbindd` is on disk or it is
  not. It is one condition rather than five because five paragraphs made a notice
  seventy lines long that a 44-row terminal could not show (the lab run that
  found this lost the title off the top of the box), and because what a reader
  needs from it is one line of package names they can install in one go. It is
  reported only on a host that already has an AD DC unit file: on Fedora and Arch
  every one of these arrives with the AD DC package itself, so before that is
  installed they would all be listed under the condition that already says to
  install it. On Debian and Ubuntu the unit comes from `samba`, which such a host
  has installed before it ever looks for `samba-tool`, and there the gaps are
  real — `samba-ad-provision`, `samba-dsdb-modules` and `samba-vfs-modules` are
  only `Recommends` of `samba` and `winbind` is only a `Suggests`. (The two python
  modules are the exception there: `python3-samba` depends on `python3-markdown`
  and `samba-common-bin` on `python3-cryptography`, so a host with `samba-tool`
  has both and the probe says so. The missing-module failures are Arch's, whose
  `samba` depends on neither.)

| What is missing | How it is read | Fedora, RHEL | Debian, Ubuntu | Arch |
| --- | --- | --- | --- | --- |
| the AD schema | `stat /usr/share/samba/setup/ad-schema` | `samba-dc-provision` | `samba-ad-provision` | `samba` |
| the AD DC daemon | the unit file on disk | `samba-dc` (`samba.service`) | `samba` (`samba-ad-dc.service`) | `samba` (`samba.service`) |
| `ldb/samba_secrets.so` | `stat` in samba's module directory | `samba-dc` | `samba-dsdb-modules` | `ldb` |
| `vfs/acl_xattr.so` | `stat` in samba's module directory | `samba` | `samba-vfs-modules` | `samba` |
| the python `cryptography` module | `python3` is asked to find it | `python3-cryptography` | `python3-cryptography` | `python-cryptography` |
| the python `markdown` module | `python3` is asked to find it | `python3-markdown` | `python3-markdown` | `python-markdown` |
| `winbindd` | `stat /usr/sbin/winbindd`, `/usr/bin/winbindd` | `samba-winbind` | `winbind` | `samba` |

Every name in that table was read off a lab guest with that distribution's own
question about the file itself — `rpm -qf` on Fedora 44, `dpkg -S` on Ubuntu
24.04.5, `pacman -Qoq` on Omarchy Server 4.0.1 — rather than from documentation.
A distribution that is not in the table is told the path and nothing else.

What each one costs if it is not caught here, which is how each was found:

- `ldb/samba_secrets.so` — provision reaches `secrets.ldb`, says
  `Module [samba_secrets] not found`, then dies on `'NoneType' object has no
  attribute 'startswith'`.
- `vfs/acl_xattr.so` — `Error loading module …/vfs/acl_xattr.so`, then
  `create_conn_struct: smbd_vfs_init failed`, at the very end of a provision
  that had otherwise worked.
- `cryptography` — no `samba-tool` subcommand runs at all, because samba-tool
  builds its own subcommand table through it.
- `markdown` — provision gets as far as `Fixing provision GUIDs` and dies in
  `forest_update.py` with `ModuleNotFoundError: No module named 'markdown'`.
- `winbindd` — the provision succeeds and nothing serves the domain: a samba
  running as an AD DC forks `winbindd`, and when the binary is absent the unit
  dies in the same second with `/usr/sbin/winbindd: Failed to exec child`.

The python check is the one that is not a `stat`, and it is a process on purpose:
where a python module lives is the interpreter's business, and the three guests
put the same two modules in three different directories. Asking `python3` to
find them costs 27ms on each of them, and `samba-tool`'s shebang is
`#!/usr/bin/python3` on all three, so the question is the one samba-tool would
otherwise answer with a traceback. Where there is no `python3` to ask, nothing is
reported: absence cannot be proved without an interpreter, and a false condition
here would block the wizard.

### The wizard

1. **Realm** — the domain's DNS name (`lab.example`), validated as one.
2. **NetBIOS domain** — the short name, prefilled from the realm's first label.
3. **DNS backend** — `SAMBA_INTERNAL` (default) or `BIND9_DLZ`.
4. **DNS forwarder** — optional, internal backend only. `samba-tool domain
   provision` has no forwarder flag, so it reaches samba as the smb.conf
   parameter it is: one `--option=dns forwarder=…` argument, quoted in the
   preview because its parameter name carries a space.
5. **The address this controller serves** — a picker over this host's own IPv4
   addresses, the one on the default route preselected, skipped entirely when
   there is only one. Left unanswered, samba picks an address itself and only
   warns about it, and the address it picked is what goes into the DC's own A
   record — the record every member of the domain resolves. It becomes
   `--host-ip=…`, and it decides one more thing without asking: the controller is
   bound to the interface that owns it plus loopback
   (`--option=interfaces=lo <iface>`, `--option=bind interfaces only=yes`), so
   the internal DNS server claims port 53 only there. On a host where libvirt's
   or a docker bridge's dnsmasq already holds it elsewhere, a controller that
   tried every address would never start.
6. **Type the realm back** — a provision decides everything after it, so it
   gets a second, deliberate confirmation before the usual command preview.

The addresses are read in pure Go — `net.Interfaces`, plus an unconnected UDP
socket toward a documentation address to learn which source address the kernel
would use. No command runs to answer a question.

### After it runs

The result screen shows the one-time Administrator password, the facts provision
summarised, and the WARNING lines it printed — that is where "More than one IPv4
address found" and "No IPv6 address will be assigned" appear, and they are facts
about the domain that was just created.

Then it offers the follow-ups as an ordered chain, one preview and one confirm
each, in the order that ends with a running controller:

1. `install -m 644 /var/lib/samba/private/krb5.conf /etc/krb5.conf.d/samba-dc.conf`
   — only where the host has that include directory and its `/etc/krb5.conf`
   sets no `default_realm` of its own. On a samba built against the MIT KDC,
   which is Fedora's build, the KDC reads `/etc/krb5.conf`, and Fedora ships it
   with `default_realm` commented out: without this the unit exits at startup
   with nothing in the journal but `mitkdc child process exited`. Where the
   drop-in does not apply, the screen keeps the old note — merge the generated
   file yourself, and do not symlink it.
2. `systemctl disable --now smbd.service nmbd.service` — only where those units
   are both installed and enabled, which on a Debian or Ubuntu host is what
   installing `samba` left behind. They hold 139 and 445; a domain controller
   runs its own `smbd` on those ports, so while they are up the AD DC unit
   starts and exits again with nothing in the journal but `smbd child process
   exited`. It is **one** step and not two, and it names only the units this host
   actually has enabled: they are one fact — this host serves files — and a
   confirm that stopped `smbd` and left `nmbd` enabled would leave the
   controller exactly as unable to start, so it would be a dialog that cannot
   succeed on its own.
3. `systemctl enable --now samba-ad-dc.service` (or `samba.service` — the unit is
   detected per distribution), which starts the controller.

Which of the three a host is offered is a fact about the distribution, and the
lab matrix measured it: Fedora 44 needs the first and not the second (MIT KDC,
`smbd.service` not shipped), Ubuntu 24.04 the second and not the first (embedded
Heimdal, no `/etc/krb5.conf.d`), Omarchy Server 4.0.1 neither. `--demo-fresh`
walks all three, because the fake machine has both conditions and refuses to
start its unit until each has been cleared.

A provision that fails gets the same full-screen notice a successful one does,
with the tail of its transcript: it prints hundreds of lines before it fails and
the reason is in them, which a single status line could not show.

The wizard is refused, at the key and again in the backend, on a host that
already serves a domain: this tool creates a domain, it does not replace one.

## What it does not do

It does not join or demote a domain. Both touch a trust relationship with
another controller, and their command lines are worth reading in a shell where
they can be checked twice.

It is also not a file server tool. The Samba on a domain controller also serves
`sysvol` and `netlogon`, but shares, sessions and the password database are
[tui-samba](https://github.com/tui-tools/tui-samba), and the two do not overlap.

## The six screens

| Screen | What it reads | What it can change |
| --- | --- | --- |
| **domain** | `domain info`, `domain level show`, `testparm`, `domain passwordsettings show` | provision a new domain (P, when none exists — preflight, wizard and the follow-up steps), edit a password-policy setting (e) |
| **users** | `user list`, then `user show` per row | create, delete, enable, suspend, reset password, set expiry |
| **groups** | `group list`, then `group listmembers` per row | create, delete, add member, remove member |
| **computers** | `computer list`, then `computer show` per row | nothing yet |
| **dns** | `dns query <server> <zone> @ ALL -P` | add record, delete record |
| **replication** | `drs showrepl <server> -P` | nothing |

The lists are cheap and the detail is not: `user show` is one samba-tool
process per account, and a domain with five hundred of them would take minutes
to open if the tool insisted on knowing everything before drawing anything. So
the tables come from the lists, and the detail is read for the rows actually on
screen, one at a time, following the cursor. A row that has not been read yet
says `?` rather than guessing.

## Install

<!-- install:start -->
<!-- Generated by tui-kit/tools/render-install.py from tool.json. -->
<!-- Edit the manifest, then run `make readme`. -->

### From source

```sh
git clone https://github.com/tui-tools/tui-dc
cd tui-dc && make demo
```

`make demo` runs against a sample domain, so it needs no Samba at all.

Not packaged for these yet; the static binary works everywhere in the meantime.

### Arch Linux — coming soon

Needs the tui-tools repository, which is a [one-time
setup](https://tui.tools/install/).

The one-liner detects the distribution and adds the repository and its signing
key:

```sh
curl -fsSL https://pkgs.tui.tools/install.sh | sh
```

Piping a script into a shell is not this family's style, so here is the same
setup by hand — read it, or read the script first with `curl -fsSL
https://pkgs.tui.tools/install.sh -o install.sh`:

```sh
curl -fsSL -o /tmp/tui-tools.asc https://pkgs.tui.tools/pubkey.asc
sudo pacman-key --add /tmp/tui-tools.asc
sudo pacman-key --lsign-key \
  "$(gpg --show-keys --with-colons /tmp/tui-tools.asc | awk -F: '/^fpr:/{print $10; exit}')"
printf '[tui-tools]\nServer = https://pkgs.tui.tools/arch/$arch\n' \
  | sudo tee -a /etc/pacman.conf
sudo pacman -Sy
```

Then, and for every other tool in the family:

```sh
sudo pacman -S tui-dc
```

Not released yet. The channel turns available once the first release lands in
pkgs.tui.tools.

### Debian and Ubuntu — coming soon

Needs the tui-tools repository, which is a [one-time
setup](https://tui.tools/install/).

The one-liner detects the distribution and adds the repository and its signing
key:

```sh
curl -fsSL https://pkgs.tui.tools/install.sh | sh
```

Piping a script into a shell is not this family's style, so here is the same
setup by hand — read it, or read the script first with `curl -fsSL
https://pkgs.tui.tools/install.sh -o install.sh`:

```sh
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://pkgs.tui.tools/pubkey.asc \
  | sudo gpg --dearmor -o /etc/apt/keyrings/tui-tools.gpg
echo "deb [signed-by=/etc/apt/keyrings/tui-tools.gpg] https://pkgs.tui.tools/deb stable main" \
  | sudo tee /etc/apt/sources.list.d/tui-tools.list
sudo apt update
```

Then, and for every other tool in the family:

```sh
sudo apt install tui-dc
```

Not released yet. The channel turns available once the first release lands in
pkgs.tui.tools.

### Fedora and RHEL — coming soon

Needs the tui-tools repository, which is a [one-time
setup](https://tui.tools/install/).

The one-liner detects the distribution and adds the repository and its signing
key:

```sh
curl -fsSL https://pkgs.tui.tools/install.sh | sh
```

Piping a script into a shell is not this family's style, so here is the same
setup by hand — read it, or read the script first with `curl -fsSL
https://pkgs.tui.tools/install.sh -o install.sh`:

```sh
sudo rpm --import https://pkgs.tui.tools/pubkey.asc
sudo curl -fsSL -o /etc/yum.repos.d/tui-tools.repo https://pkgs.tui.tools/rpm/tui-tools.repo
sudo dnf makecache
```

Then, and for every other tool in the family:

```sh
sudo dnf install tui-dc
```

Not released yet. The channel turns available once the first release lands in
pkgs.tui.tools.

### Any distribution, static binary — coming soon

```sh
curl -fsSL https://github.com/tui-tools/tui-dc/releases/download/v0.2.1/tui-dc_0.2.1_linux_amd64.tar.gz | tar -xz tui-dc
sudo install -m0755 tui-dc /usr/local/bin/tui-dc
```

Not released yet.

### Verify a download

Every release of `tui-dc` ships a `checksums.txt`. Check an archive against it
before installing:

```sh
sha256sum -c checksums.txt --ignore-missing
```

Website: https://tui.tools/tools/tui-dc/
<!-- install:end -->

## Configuration

```toml
# /etc/tui-dc/config.toml or ~/.config/tui-dc/config.toml
server = "127.0.0.1"   # the controller to read; this host by default
sudo   = "sudo -n"     # "" to run without escalation
theme  = ""            # path to an Omarchy-style colors.toml
```

Every key can also be set as `TUI_DC_SERVER`, `TUI_DC_SUDO`, `TUI_DC_THEME`, or
passed as a flag, which wins over both.

Reads escalate. samba-tool opens the directory database directly and that
database is readable only by root, so there is no unprivileged read path to fall
back on. A machine where escalation is refused says so at startup rather than
showing an empty domain.

Two commands are not a database read: `dns` talks DNS RPC and `drs` talks DRS,
both authenticated calls to the running controller, which root alone does not
satisfy. Those four commands — the zone read, the replication read, and the two
record writes — carry samba-tool's `-P`, so they authenticate as this machine's
own account out of the secrets this host already holds. No password is typed or
stored, and the flag is part of the command line the confirm dialog shows.

## Non-interactive

```sh
tui-dc --check    # the read path, once, as JSON — no UI, no changes
tui-dc --report   # the versions and machine facts a bug report needs
tui-dc --demo     # a sample domain, no Samba required
```

`--check` is safe to run anywhere, including in CI against a production-shaped
machine: it never builds and never runs a mutation. A machine with no Samba on
it answers `"installed": false` with the reason beside it, which is the true
answer for most machines and not a failure.

## Compatibility

<!-- compat:start -->
<!-- Generated by tui-kit/tools/render-compat.py from tool.json. -->
<!-- Edit the manifest, then run `make readme`. -->

`tui-dc` probes its backend once at startup and shows the version in the
header. A version nobody has tested is marked `(untested)` there rather than
hidden; one below the minimum is marked as such and the tool still runs.

### samba

| | |
| --- | --- |
| Binary | `samba-tool` |
| Version read with | `samba-tool --version` |
| Minimum | 4.13 |
| Tested | `4.19.5`, `4.24.6`, `4.24.7` |
| Version-gated features | `computer-subcommand` (since 4.8) |

| Versions | What changes |
| --- | --- |
| `<4.8` | `samba-tool computer` does not exist, so the computers screen is empty |
| `<4.13` | untested: the output of `domain info`, `dns query` and `drs showrepl` has changed shape across releases, and the parsers are checked against live controllers running 4.19.5, 4.24.6 and 4.24.7; nothing older, and nothing between 4.13 and 4.19, has been exercised |

The tested versions are generated from `compat/results.jsonl`, which the tool's
own smoke test appends to when it runs against a real machine in
[tui-lab](https://github.com/tui-tools/tui-lab).
<!-- compat:end -->

## What is still missing

An explicit list, because a gap you can read is cheaper than one you discover:

- **Three fixtures are still constructed rather than captured.** `samba-tool
  domain provision` cannot complete inside an unprivileged container — it panics
  at the sysvol ACL step — so the fixtures for `domain info`, `dns query` and
  `drs showrepl` are written from the documented formats rather than captured.
  Everything else in [`internal/samba/testdata`](internal/samba/testdata) is
  real output from a throwaway DC, and that directory's README says which is
  which. The tool itself has now run against a live domain controller on each of
  the three guests [tui-lab](https://github.com/tui-tools/tui-lab) covers, which
  is where the versions in `tested` come from; capturing those three fixtures
  from one of those controllers is still open.
- **The computers screen is read-only.** `samba-tool computer create` and
  `computer delete` are obvious next actions and were left out of phase one
  deliberately: a machine account deleted by accident takes a domain member off
  the domain.
- **No FSMO, no sites, no GPO, no trusts, no OU tree.** `samba-tool fsmo show`
  belongs on the domain screen and is the most obvious gap. The accounts and
  groups screens show a flat list, so an OU structure is invisible.
- **No screenshots from a real domain.** The ones above are `--demo`.

## Development

```sh
make check         # gofmt, vet, the exec boundary, golangci-lint, tests
make demo          # the UI, against the sample domain
make manifest      # validate tool.json against the family schema
make screenshots   # re-render the README frames from --demo
go test -fuzz FuzzParseShowRepl ./internal/samba/
```

`make check` is what CI runs. The exec boundary check is the one worth
understanding: only `internal/samba` may start a process, so the command the
confirm dialog showed and the command that runs are provably the same value.

| In this repository | What it is |
| --- | --- |
| `cmd/tui-dc/main.go` | Flags, configuration, backend selection, program start |
| `cmd/tui-dc/app.go` | The Bubble Tea model: one flat update loop over six screens |
| `cmd/tui-dc/view.go` | The bands every screen draws, and the six tables |
| `cmd/tui-dc/check.go` | `--check`: the read path as JSON |
| `cmd/tui-dc/report.go` | `--report`: the block a bug report pastes |
| `internal/directory/` | The model, the action table, and the one function that builds a command line |
| `internal/samba/` | The only place a process is started: samba-tool, its parsers, and the fake |
| `internal/samba/testdata/` | Real captured samba-tool output, and the three constructed files, labelled |
| `test/smoke.sh` | The assertions the lab runs against a real machine |

## License

MIT.
