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
| `dns-query.txt` | `samba-tool dns query <server> <zone> @ ALL` |
| `drs-showrepl.txt` | `samba-tool drs showrepl` |

`drs-showrepl.txt` deliberately contains one failing partition, because a
replication screen that has only ever been fed healthy output is a replication
screen nobody has tested.

Replacing them is the first thing to do once a real DC is in the lab; the
parser tests should then be re-run unchanged.
