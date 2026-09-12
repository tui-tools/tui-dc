# testdata

What the parsers in `internal/samba` are fed, and where each file came from.
Provenance matters here: a fixture somebody wrote from memory is a test of the
author's memory, not of Samba, and the difference has to be visible.

The realm throughout is the documentation one — `lab.example` / `LAB`, hosts
`dc1`, `ws01`, `ws02`, addresses in `10.10.0.0/24`. Nothing real, ever.

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

`domain-provision.txt` is a `samba-tool domain provision` transcript from
samba 4.24.6 on Fedora 44, which is the shape that matters: on 4.24 samba
prints its whole closing summary through its own logger, so every fact arrives
behind an `INFO <date> pid:<n> <file> #<line>:` prefix rather than on a bare
line. A parser written against the bare shape silently finds none of them, and
the Administrator password is the one fact a provision prints that exists
nowhere else afterwards.

Scrubbed, as always, and more carefully here: the generated Administrator
password is replaced by an obviously fake placeholder, the timestamps and pid
are flattened, and realm, hostname and domain SID are the documentation ones.
The bare-line shape older samba prints is covered by a literal in
`provision_test.go`, so both are held.

| File | Command |
| --- | --- |
| `domain-provision.txt` | `samba-tool domain provision --realm=LAB.EXAMPLE --domain=LAB --server-role=dc --dns-backend=SAMBA_INTERNAL` |

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
