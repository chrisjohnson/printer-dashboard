---
id: K-090
title: Revert token-cache docker volume to a shared path and consolidate config.yaml into it
initiative_id: null
claimed_by: gentle-loris-hazel
claimed_at: 2026-07-20T02:29Z
blocks: null
blocked_by: null
related_cards: [K-088, K-089]
---

# K-090 — Revert token-cache docker volume to a shared path and consolidate config.yaml into it

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

**Human follow-up (2026-07-20), merged into this card's scope:** confirmed
comfortable sharing `~/.printer-dashboard` across workers (accepting the
narrow, self-healing write-collision window). Additionally wants
`config.yaml` consolidated into that same shared directory rather than
living per-worktree/per-checkout — i.e. `docker run` should mount
`config.yaml` from `${HOME}/.printer-dashboard/config.yaml` instead of
`$(pwd)/config.yaml`. The human has real printer config data at
`/Users/chrisjohnson/src/chrisjohnson/printer-dashboard/config.yaml` (the
main checkout root — NOT the placeholder one created ad hoc in the
worktree under K-089) and wants that copied into
`~/.printer-dashboard/config.yaml` as the new canonical location.
Confirmed on disk: `~/.printer-dashboard/` already exists predating K-088
(contains real cached Bambu tokens from 2026-07-08), and the real
`config.yaml` exists at the main checkout root.

## Plan
1. [x] Implementer: updated both files' docker run commands — token cache
   mount reverted to shared `${HOME}/.printer-dashboard`, config.yaml
   mount changed to `${HOME}/.printer-dashboard/config.yaml`, first-time
   setup snippet updated to target the shared path.
2. [x] Implementer: updated surrounding prose in both files explaining
   the shared-vs-per-worker rationale.
3. [x] Implementer: copied real config from main checkout root to
   `~/.printer-dashboard/config.yaml` (no backup needed, nothing existed
   at destination). Did not log contents.
4. [x] Implementer: restarted the K-089 container with corrected mounts.
   **New host port: 55002.** Logs confirm healthy — no crash-loop, and
   critically: it loaded the existing cached Bambu token from the shared
   cache and did NOT need a fresh login, validating the whole premise of
   this fix. All 3 real printers registered, MQTT connected, camera
   connected.
5. [x] Implementer: full test suite passes, committed. I reviewed the
   diff, rebased for a clean PR, re-verified tests myself, pushed, PR:
   https://github.com/chrisjohnson/printer-dashboard/pull/8

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-20T02:35Z — filed to backlog per §2 (self-correction of my own K-088 work, discovered via human question during K-089); human said hold off on fixing now, not started -->
<!-- signal: gentle-loris-hazel 2026-07-20T02:29Z — human confirmed + expanded scope (config.yaml consolidation), promoting to now/ and claiming -->
<!-- signal: gentle-loris-hazel 2026-07-20T03:05Z — done, PR #8 open, container restarted on port 55002 with real config, moved to done/ -->

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
  K-089's launch) — human initially said hold off on fixing right now.
- 2026-07-20 — gentle-loris-hazel: human came back and confirmed comfort
  with the shared-path tradeoff, and expanded scope to also consolidate
  config.yaml into `~/.printer-dashboard`, using their real config at the
  main checkout root. Promoting to now/ and claiming per §2 (human
  request is the promotion).
- 2026-07-20 — gentle-loris-hazel: Implementer completed both the docs
  change and the operational work. The restarted container's own logs
  are the empirical proof this fix is correct: it found and reused the
  pre-existing cached token from `~/.printer-dashboard` (predating K-088,
  from 2026-07-08) rather than requiring a fresh Bambu Cloud login — the
  exact scenario this card exists to fix. PR #8 opened, build/vet/test
  all pass. Closing as done.

## Handoff notes
PR #8 open against main, not yet merged. Dashboard is live at
`http://192.168.1.170:55002` (port changed from K-089's 55001 due to the
container restart) with real printer data now, since it's using the
human's actual `config.yaml`.
