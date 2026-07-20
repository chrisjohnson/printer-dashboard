---
id: K-089
title: Build and launch app via docker for human LAN access
initiative_id: null
claimed_by: gentle-loris-hazel
claimed_at: 2026-07-20T01:57Z
blocks: null
blocked_by: null
related_cards: [K-088]
---

# K-089 — Build and launch app via docker for human LAN access

## Context

Human-requested (2026-07-20): build and launch the app using the updated
docker instructions from `AGENTS.md`/`README.md` (K-088's concurrency-safe
naming scheme), and report back a URL using host `192.168.1.170` (human is
connecting from another machine on the same LAN) so they can connect and
test.

## Plan
1. [ ] Implementer: run the updated `AGENTS.md`/`README.md` docker
   build/run commands (post-K-088: derives `NAME`/volume from worktree,
   random host port via `-p 0:8080`, looked up via `docker port`).
2. [ ] Implementer: confirm the container is actually up and serving
   (e.g. curl the health endpoint from inside/outside if possible).
3. [ ] Implementer: report back the assigned host port so the final URL
   `http://192.168.1.170:<port>` can be given to the human.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-20T01:57Z — claiming (human request, direct promotion per §2), dispatching implementer to build+launch -->

## Working context
- Uses the K-088 naming/port scheme (now merged to main) — see
  `AGENTS.md`/`README.md` "Running with Docker" sections for exact
  commands.

## Decision log
- 2026-07-20 — gentle-loris-hazel: filed directly in now/ per §2
  (human-requested — their request is the promotion), claimed immediately
  to dispatch build/launch.

## Handoff notes
Not started. Dispatching Implementer next.
