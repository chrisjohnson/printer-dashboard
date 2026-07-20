---
id: K-090
title: Revert Bambu token-cache docker volume to a shared path (K-088 over-isolated it)
initiative_id: null
claimed_by: null
claimed_at: null
blocks: null
blocked_by: null
related_cards: [K-088, K-089]
---

# K-090 — Revert Bambu token-cache docker volume to a shared path (K-088 over-isolated it)

## Context

Human-reported (2026-07-20), while using the app built/launched under K-089:
K-088's concurrency-safe docker naming scheme suffixed the Bambu token-cache
volume mount with the worktree name
(`${HOME}/.printer-dashboard-${WORKTREE:-default}`), same as the image/
container name. That's the wrong call for this specific mount.

Researched and confirmed: the token cache
(`internal/printers/bambu/auth.go` `SaveToken`/`DefaultTokenDir`) is a
small (~200 byte) per-account JSON file (access token, user_id, region,
email, expires_at), written **only at login time** — once at first login,
or again only if the app restarts and finds the cached token expired/
rejected (`internal/server/server.go:130-169`). It is NOT written per-
request or on a refresh timer (`EnsureAuthenticated` exists but is unused
dead code). Writes are a plain `os.WriteFile` (not atomic, no locking),
but given how rarely writes happen, real concurrent-write collisions
between two fleet workers would be rare, and even a torn write is self-
healing (a corrupt/rejected token just triggers an automatic re-login).

Per-worktree isolation of this mount means every new fleet worker starts
with **zero cached token** and must do a full Bambu Cloud login (real
credentials, possibly 2FA) from scratch, instead of reusing a session
another worker already established for the same cloud account — a real,
recurring cost for a collision risk that was already small and self-
healing.

Unlike the token cache, the **image name, container name, and host port**
(also part of K-088's fix) genuinely need per-worker isolation — they're
live resource identifiers that collide destructively (one worker's
`docker rm -f` killing another's running container) if shared. Only the
token-cache volume mount should be reverted.

## Plan
1. [ ] Implementer: update `AGENTS.md` and `README.md`'s "Running with
   Docker" docker run command — change
   `-v "${HOME}/.printer-dashboard-${WORKTREE:-default}:/home/app/.printer-dashboard:rw"`
   back to a shared path, e.g.
   `-v "${HOME}/.printer-dashboard:/home/app/.printer-dashboard:rw"`
   (drop the `${WORKTREE:-default}` suffix for this one mount only — keep
   it for image/container name).
2. [ ] Implementer: update surrounding prose in both files explaining why
   this one mount is intentionally shared while image/container/port are
   not (token cache is account-scoped and rarely-written/self-healing;
   image/container/port are live resources that collide destructively).
3. [ ] Implementer: if a container built under the old (per-worktree)
   scheme is still running, note in the PR/report that it'll need a
   restart with the corrected mount to pick up the shared path — don't
   restart it automatically as part of this card unless asked.
4. [ ] Run full test suite (docs-only change, but sanity check nothing
   else broke), commit, push, open PR.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-20T02:35Z — filed to backlog per §2 (self-correction of my own K-088 work, discovered via human question during K-089); human said hold off on fixing now, not started -->

## Working context
- Files: `AGENTS.md` (~lines 12-44), `README.md` (~lines 37-71) — both
  contain the same docker run command block, edited identically in K-088.
- Research citations: `internal/printers/bambu/auth.go:24` (`DefaultTokenDir`),
  `:148-190` (`SaveToken`/path building), `:320,422,477,528` (the 4
  login-completion call sites that trigger a save), `internal/server/server.go:130-169`
  (startup token load + re-login-on-expiry).

## Decision log
- 2026-07-20 — gentle-loris-hazel: filed to backlog per §2 (self-discovered
  correction to my own recent K-088 work, from a human question during
  K-089's launch) — human said hold off on fixing right now, so filing for
  later rather than acting immediately.

## Handoff notes
Not started, human explicitly said hold off for now (2026-07-20). When
picked up: it's a small, well-scoped docs fix (2 files, one line each,
plus prose). The currently-running container from K-089
(`printer-dashboard-gentle-loris-hazel`, host port 55001) is using the
old per-worktree token-cache path — will need a restart to pick up the
shared path once this lands, if the human still wants that container.
