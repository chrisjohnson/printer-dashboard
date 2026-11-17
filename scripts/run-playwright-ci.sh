#!/bin/sh
# Runs the Playwright suite end to end: installs Node dependencies if they are
# missing, then `npx playwright test`, whose webServer hook brings up the stub
# Snapmaker and the dashboard under test (tests/run-test-server.sh).
#
# The environment is expected to provide Node and a Playwright browser — in CI
# that is actions/setup-node plus `npx playwright install --with-deps chromium`;
# locally it is the mcr.microsoft.com/playwright image, which ships the browsers.
#
# Shared by CI and local container runs so there is one definition of "the suite
# passed" and the two cannot drift apart.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

if [ ! -d node_modules ]; then
  echo "run-playwright-ci: no node_modules, running npm ci" >&2
  npm ci
fi

exec npx playwright test "$@"
