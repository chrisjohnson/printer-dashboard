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
1. [ ] Researcher: read K-084's README/AGENTS.md docker instructions and
   confirm exactly where the fixed name collides (image name, container
   name, and/or published port).
2. [ ] Researcher: decide naming scheme for concurrency-safety — likely
   derive container/image name (and port, if fixed) from worktree directory
   name or branch, so parallel `docker build`/`docker run` don't share
   state.
3. [ ] Implementer: update README.md and root AGENTS.md docker instructions
   to the new scheme; keep it as simple as the single-worker case where only
   one worktree is active.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: swift-panda-dusk 2026-07-16T09:26Z — filed per human request, not started -->
<!-- signal: gentle-loris-hazel 2026-07-16T14:10Z — claiming, dispatching researcher to confirm collision points and naming scheme -->

## Decision log
- 2026-07-16 — swift-panda-dusk: filed to backlog per §2 (human-requested
  ticket, orchestrator role never claims — human to promote to now/ when
  ready per §4a).

## Handoff notes
Not started. Related to K-084 (established the fixed-name convention this
card needs to fix) and K-012 (docker-compose for multi-service deployment —
may or may not be the same solution space, worth checking when picked up).
