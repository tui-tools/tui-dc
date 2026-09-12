# testdata

What the parsers in `internal/samba` are fed, and where each file came from.
Provenance matters here: a fixture somebody wrote from memory is a test of the
author's memory, not of Samba, and the difference has to be visible.

The realm throughout is the documentation one — `lab.example` / `LAB`, hosts
`dc1`, `ws01`, `ws02`, addresses in `10.10.0.0/24`. Nothing real, ever.

The one exception is `domain-provision.txt`, which is a transcript of a real
provision and is kept as it was printed: its host is the lab guest's own
`fedora` and its addresses are the guest's own `10.0.2.x`, which name a
throwaway VM and nothing else.

## Captured

Real `samba-tool` output, captured from a throwaway Samba AD DC provisioned in
a rootless podman container (`debian:trixie`, samba
`4.22.10-Debian-4.22.10+dfsg-0+deb13u2`) with:

```sh
samba-tool domain provision --use-rfc2307 --realm=LAB.EXAMPLE --domain=LAB \
  --server-role=dc --dns-backend=SAMBA_INTERNAL
```

then `user create` / `user disable` / `group add` / `group addmembers` /
`computer create` for the sample objects. The container was removed afterwards.

The only edit applied is a scrub: the container's generated hostname became
`DC1` / `dc1`, and its container-network DNS forwarder became `10.10.0.1`.

| File | Command |
| --- | --- |
| `version.txt` | `samba-tool --version` |
| `domain-level-show.txt` | `samba-tool domain level show` |
| `user-list.txt` | `samba-tool user list` |
| `user-show-alice.txt` | `samba-tool user show alice` |
| `user-show-administrator.txt` | `samba-tool user show Administrator` |
| `group-list.txt` | `samba-tool group list` |
| `group-listmembers-domain-admins.txt` | `samba-tool group listmembers "Domain Admins"` |
| `group-listmembers-helpdesk.txt` | `samba-tool group listmembers Helpdesk` |
| `computer-list.txt` | `samba-tool computer list` |
| `computer-show-ws01.txt` | `samba-tool computer show WS01` |
| `testparm.txt` | `samba-tool testparm --suppress-prompt` |

Three more are captured failures, which are worth as much as the successes:
they are exactly what the tool sees on a machine where the DC is not answering,
and the read path has to survive them without claiming the domain is empty.

| File | What it is |
| --- | --- |
| `domain-info-unreachable.txt` | `domain info` when no controller answers the CLDAP query |
| `dns-query-refused.txt` | `dns query` when the DNS RPC server refuses the connection |
| `drs-showrepl-failed.txt` | `drs showrepl` when the DRS connection cannot be made |

### The provision transcript

`domain-provision.txt` is the transcript of a provision that really happened:
an acceptance run in [tui-lab](https://github.com/tui-tools/tui-lab) on a
Fedora 44 guest (2 cpu, 4 GB, SELinux enforcing) with samba 4.24.6, captured on
2026-09-12 with the exact command line the wizard builds:

```sh
samba-tool domain provision --realm=LAB.EXAMPLE --domain=LAB \
  --server-role=dc --dns-backend=SAMBA_INTERNAL --host-ip=10.0.2.15 \
  '--option=interfaces=lo enp0s4' '--option=bind interfaces only=yes' \
  '--option=dns forwarder=10.0.2.3'
```

It was run outside the tool, on a guest restored to the same pre-provision
snapshot the acceptance flow starts from, so the file is the tool's own input
without the tool in the way. Exit status 0.

This is the shape that matters, and the bug the fixture pins: on 4.24 samba
prints its whole closing summary through its own logger, so every fact arrives
behind an `INFO <date> pid:<n> <file> #<line>:` prefix rather than on a bare
line. A parser written against the bare shape silently finds none of them, and
the Administrator password is the one fact a provision prints that exists
nowhere else afterwards. Nothing short of a real transcript proves that: the
prefix, the `pid:`, the interleaved bare `Repacking database …` lines and the
hundreds of `Applied Forest Update` lines are all samba's, not an author's idea
of samba's.

Scrubbed, and only here:

- the generated Administrator password became `FAKE-password-not-a-real-one`,
  keeping samba's own column alignment so the screen's shape is still tested;
- the domain SID became `S-1-5-21-1111111111-2222222222-3333333333`;
- the gkdi/gmsa root key guid became the nil guid — which also caught the
  guid on the last `Applied Domain Update 89` line, since that line happened to
  carry the same value the scrub replaced. It is a public AD schema constant,
  not a secret, and no parser here reads it.

No line was reordered, removed or reflowed, and the timestamps and pid are the
run's own. The bare-line shape older samba prints is covered by a literal in
`provision_test.go`, so both are held. A successful provision given `--host-ip`
prints no `WARNING` line at all — samba has no address to guess and 4.24.6 logs
its IPv6 lookup at `INFO` — so the warning path is held by the captured refusal
below and by literals in `preflight_test.go` rather than by this file.

| File | Command |
| --- | --- |
| `domain-provision.txt` | `samba-tool domain provision` as above, on a tui-lab Fedora 44 guest |

### The two refusals the preflight exists for

Both captured from the same Fedora 44 host, samba 4.24.6, each from a provision
that was started and refused — which is the whole point of having them: these
are the bytes the tool has to recognise before a wizard is worth opening, and
the second one is also where samba's WARNING lines live.

`domain-provision-role-refused.txt` is a provision started with the
distribution's own `/etc/samba/smb.conf` still in place (`security = user`,
which `testparm` resolves to `server role = auto`). Nothing had been written
when it refused.

`domain-provision-schema-missing.txt` is a provision on a host with `samba` and
`samba-tools` installed and `samba-dc-provision` not: it runs for most of a
minute and then raises a bare `FileNotFoundError` on one of the AD schema `.ldf`
files. It was captured on a host with several IPv4 addresses, so it also carries
the two WARNING lines the result screen repeats — the chosen address and the
absent IPv6.

Scrubbed the same way as the transcript above: timestamps and pid flattened,
addresses replaced with documentation ones (`192.168.10.0/24`), realm and host
names the documentation ones. The Python traceback frames are samba's own file
paths, which name no machine.

| File | What it is |
| --- | --- |
| `domain-provision-role-refused.txt` | `domain provision` refused because smb.conf resolves to another server role |
| `domain-provision-schema-missing.txt` | `domain provision` dying on the missing AD schema, on a multi-address host |

## Captured values, reproduced preamble

Two files are a hybrid, and the split has to be stated because the answer in
them is real and the lines around it are not.

`samba-tool testparm --suppress-prompt` prints only the parameters smb.conf
sets, so on a host whose smb.conf leaves the role derived — `security = user`,
which every distribution ships — the role is absent from `testparm.txt`'s shape
entirely. The read path therefore asks for that one parameter by name, and
these two files are what it has to parse:

| File | Command it stands for |
| --- | --- |
| `testparm-parameter-role-auto.txt` | `samba-tool testparm --suppress-prompt --parameter-name="server role"` against a distribution's own smb.conf |
| `testparm-parameter-role-dc.txt` | the same command on a provisioned controller |

The **values** — `auto` and `active directory domain controller` — are the two
answers read on the lab's Fedora 44 guest with samba 4.24.6 and recorded in
[tui-dc#17](https://github.com/tui-tools/tui-dc/issues/17), the issue these
files exist for.

The **lines around them** are not that guest's bytes. testparm logs its
preamble through samba's own logger, and the two `INFO … Loaded …` lines here
are reproduced in the prefix shape `testparm.txt` captured — same logger
format, this file's own flattened timestamp and pid. The `WARNING` line in the
`-dc` file is `testparm.txt`'s own acl_xattr warning, kept here deliberately
rather than because that run was seen to print it: its text contains the words
"domain controller", so a parser that returned the last line outright would
read a warning as a role and make `IsDC()` true on a host that is not one. It
is in the fixture to hold that shut.

Replacing both with a verbatim capture from a guest is worth doing the next
time one is up; the values would not change, only the provenance of the
preamble.

## Constructed

Three files could **not** be captured. `samba-tool domain provision` panics at
its final sysvol-ACL step inside an unprivileged (user-namespaced) container —
`Security context active token stack underflow!` in `chown_if_needed` — which
is a known limitation of running the provision as a namespaced root rather than
real host root. It reproduced identically on samba 4.19.5 and 4.22.10, on
overlayfs and on an ext4-backed volume, with and without `acl_xattr`. The
directory database is complete by then, which is why every subcommand that
reads the local `sam.ldb` above did work; but the machine-account secrets are
not written, so the `samba` daemon never starts, and the three commands that
need a *running* controller could only produce the errors above.

These three are therefore **constructed** from the documented output formats,
and are marked as such until a real DC in
[tui-lab](https://github.com/tui-tools/tui-lab) replaces them:

| File | Command it imitates |
| --- | --- |
| `domain-info.txt` | `samba-tool domain info <server>` |
| `dns-query.txt` | `samba-tool dns query <server> <zone> @ ALL -P` |
| `drs-showrepl.txt` | `samba-tool drs showrepl <server> -P` |

`drs-showrepl.txt` deliberately contains one failing partition, because a
replication screen that has only ever been fed healthy output is a replication
screen nobody has tested.

Replacing them is the first thing to do once a real DC is in the lab; the
parser tests should then be re-run unchanged.

A fixture cannot show which command line produced it, and that is a gap worth
naming here: a zone dump parses identically whether or not the invocation could
authenticate, so these files cannot tell a correct `dns query` from one that
would have failed on a real controller for want of `-P` or a server argument.
`TestLoadDomainRPCArgv` in `load_test.go` asserts the argument lists instead,
and is what keeps those two reads honest.
