---
id: K-088
title: Docker testing strategy not safe for concurrent fleet workers
initiative_id: null
claimed_by: gentle-loris-hazel
claimed_at: 2026-07-16T14:10Z
blocks: null
blocked_by: null
related_cards: [K-084, K-012]
---

# K-088 — Docker testing strategy not safe for concurrent fleet workers

## Context

K-084 established a docker-first build/run convention: one fixed image name,
one fixed container name (`printer-dashboard`), and a `docker rm` of that
fixed container name before each `docker run`. That's fine for a single
human running one container at a time, but it does not hold up under the
fleet model where multiple worker agents run in parallel worktrees
(§3b, WIP limit 1 per agent, but many agents run concurrently across
worktrees) — two workers testing at the same time will race on the same
fixed image/container name and clobber each other's running container
mid-test.

Human-reported: "right now they'll clobber each other's container changes."

Needs a strategy where concurrent workers can each build/run/test in docker
without colliding — e.g. per-worktree or per-branch image/container naming
(derived from worktree dir or branch name), or per-worker port allocation,
while still keeping the build/run steps simple per K-084's original intent.

## Plan
1. [x] Researcher: confirmed three collision points — image name, container
   name, AND host port are all hardcoded to `printer-dashboard`/`8080` in
   both `AGENTS.md:16-23` and `README.md:46-54` (identical commands in
   both). A fourth: the `${HOME}/.printer-dashboard` volume mount is also
   shared across all worktrees (same `$HOME`). See Decision log.
2. [x] Researcher: recommended scheme — suffix image/container name and the
   token-cache volume path with the worktree pet name (falls back to plain
   `printer-dashboard` outside a fleet worktree, zero change for manual/
   single-worker use); switch the fixed `8080:8080` port publish to a
   random host port (`-p 0:8080`, queried back via `docker port`) rather
   than hand-rolling a second per-worker numbering scheme. K-012
   (docker-compose) is an empty, unstarted stub — not the same scope, not
   worth blocking on.
3. [x] Implementer: updated `AGENTS.md` and `README.md` docker commands to
   the new scheme.
4. [x] Implementer: confirmed no `.github/workflows/` exists at all in this
   repo (only a PR template) — no CI docker usage to update.
5. [x] Implementer: committed, pushed, PR opened:
   https://github.com/chrisjohnson/printer-dashboard/pull/6

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: swift-panda-dusk 2026-07-16T09:26Z — filed per human request, not started -->
<!-- signal: gentle-loris-hazel 2026-07-16T14:10Z — claiming, dispatching researcher to confirm collision points and naming scheme -->
<!-- signal: gentle-loris-hazel 2026-07-16T14:35Z — done, PR #6 open, moved to done/ -->

## Decision log
- 2026-07-16 — swift-panda-dusk: filed to backlog per §2 (human-requested
  ticket, orchestrator role never claims — human to promote to now/ when
  ready per §4a).
- 2026-07-16 — gentle-loris-hazel: Research confirmed the exact commands
  (identical in both files):
  ```
  docker build -t printer-dashboard .
  docker rm -f printer-dashboard || true
  docker run -d --name printer-dashboard \
    -p 8080:8080 \
    -v "${HOME}/.printer-dashboard:/home/app/.printer-dashboard:rw" \
    -v "$(pwd)/config.yaml:/app/config.yaml:rw" \
    printer-dashboard
  ```
  Adopting the recommended fix: derive a suffix from the worktree pet name
  (`WORKTREE=$(basename "$(pwd)")` when under `.fleet/worktrees/`, unset
  otherwise — falls back to today's exact plain-name behavior for manual/
  single-worker use, satisfying the card's "keep it as simple as the
  single-worker case" requirement):
  ```
  NAME="printer-dashboard${WORKTREE:+-$WORKTREE}"
  docker build -t "$NAME" .
  docker rm -f "$NAME" || true
  docker run -d --name "$NAME" \
    -p 0:8080 \
    -v "${HOME}/.printer-dashboard-${WORKTREE:-default}:/home/app/.printer-dashboard:rw" \
    -v "$(pwd)/config.yaml:/app/config.yaml:rw" \
    "$NAME"
  docker port "$NAME" 8080   # shows the assigned host port
  ```
  Switching `-p 8080:8080` → `-p 0:8080` (random host port, queried back)
  rather than inventing a second per-worker port-numbering scheme — this
  is manual/smoke-test tooling, not a fixed public-facing port, so a
  stable port number isn't load-bearing. K-012 (docker-compose) is an
  empty, unstarted stub in a different scope — not blocking on it.
- 2026-07-16 — gentle-loris-hazel: **caught and fixed a real bug before
  pushing.** The first Implementer pass set `WORKTREE=$(basename "$(pwd)")`
  *unconditionally* in both files' code blocks, contradicting the prose's
  claimed fallback ("leave unset for normal checkout") — run from a
  non-worktree checkout, `basename` would return the repo dir's own name
  (`printer-dashboard`), producing `NAME=printer-dashboard-printer-dashboard`
  instead of the plain fallback, breaking the card's explicit "keep it as
  simple as the single-worker case" requirement. Dispatched a follow-up fix
  (conditional `case "$(pwd)" in */.fleet/worktrees/*) ... esac`),
  re-verified myself by running the actual logic from both a real normal
  checkout and a real fleet-worktree directory on this machine — confirms
  correct fallback now.
- 2026-07-16 — gentle-loris-hazel: PR #6 opened
  (https://github.com/chrisjohnson/printer-dashboard/pull/6). Docs-only
  change; no docker daemon available in this environment to run an actual
  end-to-end concurrent-container test, noted honestly in the PR body.
  Closing as done — review/merge is outside this card's scope.

## Handoff notes
PR #6 open against main, not yet merged. If review feedback requires
changes, that's follow-up work on branch `worktree-gentle-loris-hazel-k088`.
Worth a human manually running the new commands once (build+run in two
different worktrees simultaneously) to confirm no collision in practice,
since this couldn't be verified end-to-end in this environment.
