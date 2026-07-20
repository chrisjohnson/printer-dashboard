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
1. [ ] Implementer: update `AGENTS.md` and `README.md`'s "Running with
   Docker" docker run command:
   - Token cache mount: change
     `-v "${HOME}/.printer-dashboard-${WORKTREE:-default}:/home/app/.printer-dashboard:rw"`
     back to shared: `-v "${HOME}/.printer-dashboard:/home/app/.printer-dashboard:rw"`
     (drop the `${WORKTREE:-default}` suffix here only — keep it for
     image/container name).
   - config.yaml mount: change
     `-v "$(pwd)/config.yaml:/app/config.yaml:rw"` to
     `-v "${HOME}/.printer-dashboard/config.yaml:/app/config.yaml:rw"` —
     config.yaml now lives in the shared host dir, not per-checkout.
   - Update the "copy config.example.yaml" first-time-setup step to
     target `~/.printer-dashboard/config.yaml` instead of a repo-local
     copy.
2. [ ] Implementer: update surrounding prose in both files: explain why
   token cache + config.yaml are intentionally shared (account-scoped,
   rarely-written/self-healing for the token; config is host-machine-wide
   printer configuration, not per-checkout) while image/container/port
   remain per-worker (live resources that collide destructively).
3. [ ] Implementer: copy the human's real config from
   `/Users/chrisjohnson/src/chrisjohnson/printer-dashboard/config.yaml`
   to `~/.printer-dashboard/config.yaml` (create the target dir if
   needed — it already exists here, but don't assume that generally).
   Do NOT print/log the file contents (may contain real credentials).
4. [ ] Implementer: the container already running from K-089
   (`printer-dashboard-gentle-loris-hazel`, host port 55001) was launched
   with the old per-worktree mount scheme and a placeholder config —
   restart it (`docker rm -f` + re-`docker run`) using the corrected
   shared mounts and the now-real `config.yaml`, so the human's dashboard
   actually reflects their real printers. Report the resulting host port
   (may change since it's still `-p 0:8080` random-assigned) so the human
   can get an updated URL if it differs from 55001.
5. [ ] Run full test suite (docs-only change, but sanity check nothing
   else broke), commit, push, open PR.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-20T02:35Z — filed to backlog per §2 (self-correction of my own K-088 work, discovered via human question during K-089); human said hold off on fixing now, not started -->
<!-- signal: gentle-loris-hazel 2026-07-20T02:29Z — human confirmed + expanded scope (config.yaml consolidation), promoting to now/ and claiming -->

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

## Handoff notes
Implementer dispatched by gentle-loris-hazel 2026-07-20T02:29Z, working in
`.fleet/worktrees/gentle-loris-hazel` on a fresh branch off origin/main.
Scope: doc changes (token cache + config.yaml mounts, both files) + copy
real config.yaml to `~/.printer-dashboard/config.yaml` (backing up any
existing one) + restart the K-089 container with corrected mounts. PR
not to be opened until I review the operational steps. Awaiting
completion.
