#!/bin/bash
# Backend smoke test for tui-dc, run inside a lab guest.
#
# The contract (see tui-tools/tui-lab): this script runs on the guest as the
# unprivileged lab user, escalates with `sudo -n` only, prints a short PASS/FAIL
# table and exits non-zero if anything failed. The binary under test is at
# $TUI_LAB_BIN (default: tui-dc on PATH).
#
# What a smoke test proves is that the tool reads the machine's *real* subject
# and agrees with the machine's own tooling — not that a fake renders. So the
# assertions below compare tui-dc's --check output against samba-tool itself,
# and they adapt: a guest that is not a domain controller is not a failure, it
# is most guests, and what is asserted there is that the tool says so plainly.
set -uo pipefail

bin="${TUI_LAB_BIN:-tui-dc}"
pass=0
fail=0
skip=0

# check runs one assertion. It takes a label, a command and a grep pattern the
# command's output must match. Output is captured so a failure can show it.
check() {
  local label="$1" command="$2" pattern="$3" output status
  output=$(eval "$command" 2>&1)
  status=$?
  if [[ $status -eq 0 ]] && grep -qE "$pattern" <<<"$output"; then
    printf 'PASS  %s\n' "$label"
    pass=$((pass + 1))
  else
    printf 'FAIL  %s (exit %d)\n' "$label" "$status"
    sed 's/^/      | /' <<<"$output" | head -12
    fail=$((fail + 1))
  fi
}

# skip records an assertion this guest cannot make, with the reason.
skip() {
  printf 'SKIP  %s (%s)\n' "$1" "$2"
  skip=$((skip + 1))
}

# json reads one field out of a --check run without needing jq, which is not
# installed everywhere the lab runs.
json() {
  python3 -c 'import json,sys; print(json.load(sys.stdin).get(sys.argv[1], ""))' "$1"
}

echo "--- tui-dc smoke on $(. /etc/os-release && echo "$PRETTY_NAME")"
echo "      user=$(id -un)"

# --- the report block ------------------------------------------------------
#
# --report is read-only and unprivileged, so it is smoked without sudo: a user
# who cannot escalate is exactly the one who most needs to be able to file a
# usable bug. What is asserted is that it names the backend this machine is
# actually driving, that it still answers under --demo, and that it keeps its
# privacy promise — the block goes into a public issue, so a home path or the
# host name appearing in it is a bug, not a cosmetic detail.
check "report names the backend" \
  "$bin --report" \
  '^backend: samba'

check "report says the run was live" \
  "$bin --report" \
  '^mode: live$'

check "report answers the domain-controller question" \
  "$bin --report" \
  '^this host: '

check "report works in demo mode too" \
  "$bin --demo --report" \
  '^backend: demo$'

check "and says so on the mode line" \
  "$bin --demo --report" \
  '^mode: demo'

# The distro and kernel lines are quoted from the machine's own description of
# itself, and a host named after its distribution ("fedora" on Fedora) would
# match there without anything having leaked. They are dropped before the
# search, so this stays a test of the tool rather than of the guest's hostname.
check "report leaks neither a home path nor the host name" \
  "$bin --report | grep -vE '^(distro|kernel): ' | grep -cE '/home/|$(uname -n)' || true" \
  '^0$'

# --- the demo read path ----------------------------------------------------
#
# --demo drives the fake samba-tool through the same loader and the same
# parsers as the real one, so this is a smoke test of every parser in the
# binary that needs no Samba on the guest at all.
check "demo --check is JSON with a domain in it" \
  "$bin --demo --check | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[\"realm\"], d[\"users\"], d[\"groups\"])'" \
  'lab\.example [1-9]'

check "demo replication reports the failing partition" \
  "$bin --demo --check | grep -c '\"ok\": false'" \
  '^[1-9]'

# --- the real read path ----------------------------------------------------
if ! command -v samba-tool >/dev/null 2>&1; then
  skip "check reports a machine with no samba-tool" "samba-tool is not installed"
  skip "check agrees with samba-tool user list" "samba-tool is not installed"
  skip "check agrees with samba-tool group list" "samba-tool is not installed"
  skip "record_compat" "samba-tool is not installed"
else
  check "check runs and is JSON" \
    "$bin --check | python3 -c 'import json,sys; json.load(sys.stdin); print(\"ok\")'" \
    '^ok$'

  check "check reports samba-tool as installed" \
    "$bin --check | grep '\"installed\"'" \
    'true'

  if ! sudo -n samba-tool user list >/dev/null 2>&1; then
    skip "check agrees with samba-tool user list" "this guest is not a domain controller"
    skip "check agrees with samba-tool group list" "this guest is not a domain controller"
    check "check says the domain is not reachable" \
      "$bin --check | grep '\"domainReachable\"'" \
      'false'
  else
    # The assertion that matters: the tool's count is the machine's count. A
    # parser that silently dropped a name would pass every unit test and fail
    # here, which is the whole reason the lab exists.
    check "check agrees with samba-tool user list" \
      "test \$($bin --check | json users) -eq \$(sudo -n samba-tool user list | grep -c .)" \
      '^$'

    check "check agrees with samba-tool group list" \
      "test \$($bin --check | json groups) -eq \$(sudo -n samba-tool group list | grep -c .)" \
      '^$'

    check "check agrees with samba-tool computer list" \
      "test \$($bin --check | json computers) -eq \$(sudo -n samba-tool computer list | grep -c .)" \
      '^$'

    check "check names the realm samba-tool serves" \
      "$bin --check | json realm" \
      '.+\..+'

    check "check reports this host as a domain controller" \
      "$bin --check | grep '\"isDomainController\"'" \
      'true'
  fi

  # record_compat appends the version this run exercised to
  # compat/results.jsonl, which `make compat` folds into tool.json. Nothing
  # here writes a version into the manifest by hand.
  record_compat() {
    local version distro
    version=$(sudo -n samba-tool --version 2>&1 | tail -1)
    distro=$(. /etc/os-release && echo "$ID $VERSION_ID")
    mkdir -p compat
    printf '{"backend":"samba","version":"%s","distro":"%s","tool":"tui-dc","result":"%s"}\n' \
      "$version" "$distro" "$([[ $fail -eq 0 ]] && echo pass || echo fail)" \
      >>compat/results.jsonl
    printf 'INFO  recorded samba %s on %s\n' "$version" "$distro"
  }
  record_compat
fi

echo "--- tui-dc: $pass passed, $fail failed, $skip skipped"
[[ $fail -eq 0 ]]
