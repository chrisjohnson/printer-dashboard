---
id: K-084
title: Docker-first build/run strategy — README + AGENTS.md
initiative_id: null
claimed_by: faint-skunk-cairn
claimed_at: 2026-07-16T03:30Z
blocks: null
blocked_by: null
related_cards: [K-012]
---

# K-084 — Docker-first build/run strategy — README + AGENTS.md

## Context

Human request: document a docker-first strategy for building and running the
app. Keep it simple — no compose, no multi-service orchestration (that's
K-012's job if it ever gets picked up). Just:
- `docker build` and `docker run` sharing one fixed image name
- `docker run` using a fixed container name of `printer-dashboard`
- a `docker rm` (of that fixed container name) before each `docker run`, so
  re-running doesn't collide with a leftover stopped container

Confirmed with human: container name is `printer-dashboard` (not the typo
`printer-dashbaord` from the original ask).

Needs both:
1. `README.md` updated with the docker build/run instructions (human-facing
   quickstart).
2. A new `AGENTS.md` at repo root documenting this as the project's build/run
   convention for agents working in this repo (distinct from the global
   fleet `AGENTS.md` — this one is project-specific: "how to build and run
   this app").

## Plan
1. [x] Implementer: check whether a `Dockerfile` already exists at repo
   root; if not, this card needs one too (can't docker-first without an
   image to build) — add a minimal one appropriate to the app's language/
   runtime if missing.
2. [x] Implementer: update `README.md` with a docker-first build/run section:
   `docker build -t <image> .` then `docker rm -f printer-dashboard || true`
   then `docker run --name printer-dashboard <image>` (fill in real port
   mappings/env/volumes as needed by the app).
3. [x] Implementer: add `AGENTS.md` at repo root with the same convention
   stated as project guidance for agents (fixed image name, fixed container
   name `printer-dashboard`, always `docker rm` before `docker run`).
4. [x] Implementer: verify `docker build` and `docker run` actually work
   locally against the new Dockerfile before committing.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: faint-skunk-cairn 2026-07-16T03:30Z — claiming, dispatching implementer for docker-first docs -->

## Working context

- Repo root: `/Users/chrisjohnson/src/chrisjohnson/printer-dashboard`
- No existing `Dockerfile`, `docker-compose*`, or root `AGENTS.md` found as
  of card creation.
- Related: K-012 (backlog, unclaimed) wants full docker-compose multi-service
  deployment — explicitly out of scope here; this card is single-container
  only.

## Decision log
- 2026-07-16 — orchestrator (faint-skunk-cairn): created directly in now/
  per §2 (human-requested work, their request is the promotion). Claiming
  under the orchestrator session identity to dispatch an Implementer
  sub-agent immediately rather than leaving it for queue pickup, since the
  human is waiting on this synchronously.
- 2026-07-16 — Implementer: existing root `Dockerfile` was already correct
  (multi-stage, `EXPOSE 8080`, non-root) — no changes needed there. Verified
  `docker build` + `docker rm -f printer-dashboard || true` + `docker run`
  end-to-end against a placeholder `config.yaml` (app takes no env vars,
  config is YAML-file-only). Added README "Running with Docker" section and
  new root `AGENTS.md`. Committed `6dff915` on `worktree-faint-skunk-cairn`
  (README.md + AGENTS.md only). Not pushed, no PR — orchestrator to confirm
  with human before pushing per repo's general git-safety default.

## Handoff notes
Implementation + local verification complete (commit `6dff915`). Awaiting
human confirmation to push branch and open PR vs main, then move this card
to done/.
