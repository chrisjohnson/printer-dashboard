#!/bin/sh
# Starts the deterministic test fleet the Playwright suite runs against:
#
#   1. the stub Snapmaker (tests/stub/snapmaker.js) on 127.0.0.1:18081
#   2. the dashboard itself, on 127.0.0.1:18080, reading
#      tests/testdata/config.test.yaml
#
# The stub comes up first because the dashboard connects to its printers at
# startup; a card that never got an initial report renders differently from one
# that did, and the suite depends on the latter.
#
# Used as playwright.config.ts's webServer command, both locally and in CI
# (.github/workflows/playwright.yml).
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

STUB_HOST="${STUB_HOST:-127.0.0.1}"
STUB_PORT="${STUB_PORT:-18081}"
export STUB_HOST STUB_PORT

if [ ! -x ./printer-dashboard ]; then
  # CI has a Go toolchain and builds here. The container this suite is developed
  # in has none and gets the binary bind-mounted in, so only build when missing.
  echo "run-test-server: no ./printer-dashboard, building with go" >&2
  go build -o printer-dashboard .
fi

node tests/stub/snapmaker.js &
stub_pid=$!

cleanup() {
  kill "$stub_pid" 2>/dev/null || true
  wait "$stub_pid" 2>/dev/null || true
}
trap cleanup EXIT INT TERM HUP

# Wait for the stub to answer before starting the dashboard, so the dashboard's
# first status fetch succeeds instead of leaving u1 in an error state.
ready=0
i=0
while [ "$i" -lt 100 ]; do
  if node -e '
    const http = require("node:http");
    const req = http.get(
      { host: process.env.STUB_HOST, port: process.env.STUB_PORT, path: "/api/printer", timeout: 500 },
      (res) => { res.resume(); process.exit(res.statusCode === 200 ? 0 : 1); }
    );
    req.on("error", () => process.exit(1));
    req.on("timeout", () => { req.destroy(); process.exit(1); });
  ' 2>/dev/null; then
    ready=1
    break
  fi
  i=$((i + 1))
  sleep 0.05
done
if [ "$ready" -ne 1 ]; then
  echo "run-test-server: stub snapmaker never answered on ${STUB_HOST}:${STUB_PORT}" >&2
  exit 1
fi

./printer-dashboard tests/testdata/config.test.yaml &
server_pid=$!

# Forward teardown to the server explicitly: the shell is playwright's direct
# child, and a signal that only reaches the shell would otherwise leave the
# dashboard holding port 18080 for the next run.
trap 'kill "$server_pid" 2>/dev/null || true' INT TERM HUP

status=0
wait "$server_pid" || status=$?
exit "$status"
