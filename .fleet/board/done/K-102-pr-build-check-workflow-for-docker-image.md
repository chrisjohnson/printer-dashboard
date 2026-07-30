---
id: K-102
title: PR-triggered build-only Docker workflow for pre-merge feedback
initiative_id: null
claimed_by: claude
claimed_at: 2026-07-30T05:05:00Z
blocks: null
blocked_by: null
related_cards: [K-100, K-101]
---

# K-102 — PR-triggered build-only Docker workflow for pre-merge feedback

## Context

Human request: add a workflow (or variant of K-100's `docker-publish.yml`)
that builds the multi-arch Docker image on pull requests but does NOT push
to GHCR, so build breaks (like the two cgo bugs found in K-101) show up as
PR checks instead of only being discovered after merging to main and
watching a real publish run.

Requested to land on PR #17 (branch `K-101-tutk-amd64-full-audit`) directly
— this also means once pushed, the new workflow's first real run will be
against PR #17 itself, giving actual CI signal on whether the K-101 audit
fully fixed the amd64 build, before merge.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [x] Implementer: add `.github/workflows/docker-build-check.yml` that builds `linux/amd64,linux/arm64` via buildx with `push: false`, triggered on `pull_request` targeting `main`, using GHA layer cache (distinct `pr-check` scope).
2. [x] Implementer: commit on the existing `K-101-tutk-amd64-full-audit` branch (not a new branch) and push, so it lands on PR #17 and runs against it immediately.
3. [x] Human: watch the new workflow's run on PR #17 — it worked exactly as intended, catching a 3rd distinct amd64 build error (union-field access, see K-101) before merge.
4. [x] Implementer: align cache scopes so PR checks read (not write) the same GHA cache scope `docker-publish.yml` writes on `main` pushes, instead of the current isolated `scope=pr-check` — PR builds currently start cold every run (~45s+ re-downloading ffmpeg's apt deps alone). Human asked about this directly.
5. [x] Human confirmed: `docker-publish.yml` succeeded end-to-end after merge (run 30517766342), so the shared cache scope is now actually populated — future PR checks and main pushes should see real speedups.
6. [x] Implementer: add a `paths:` filter to both workflows so board-only commits (`.fleet/board/**`) don't trigger full multi-arch Docker builds — observed two wasted `docker-publish.yml` runs triggered by plain board-commit pushes to main during this session.
7. [x] Human: merged PR #18; confirmed board-only commit does not trigger docker-publish.yml.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: claude 2026-07-30T05:05:00Z — claiming, adding PR-triggered build-only check per direct human request -->
<!-- signal: claude 2026-07-30T05:20:00Z — docker-build-check.yml pushed to PR #17, awaiting its first run for real signal -->
<!-- signal: claude 2026-07-30T05:20:00Z — note: a subagent checked out K-101's branch directly in REPO_ROOT (same checkout used for board commits), causing transient working-tree drift on board files; no commits were lost (verified against origin/main), but a subagent touching a feature branch should use its own worktree, not REPO_ROOT, when REPO_ROOT is also being used for board commits -->
<!-- signal: claude 2026-07-30T05:40:00Z — PR #17 build-check caught a real 3rd bug pre-merge, exactly as intended; now aligning cache scope with docker-publish.yml per human follow-up question -->
<!-- signal: claude 2026-07-30T06:00:00Z — cache-scope fix merged via PR #17, docker-publish confirmed green post-merge; adding paths: filter next to stop board-only commits from triggering full builds -->
<!-- signal: claude 2026-07-30T06:10:00Z — PR #18 open with paths: filter for both workflows, awaiting human merge -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->
K-100's `docker-publish.yml` triggers on push to main/tags + workflow_dispatch,
no `pull_request` trigger (intentional, so untrusted PR code never gets
`secrets.GITHUB_TOKEN`/push access — see K-100 decision log). A build-only
`pull_request`-triggered workflow needs no registry login/secrets at all, so
it's safe to run on any PR including forks.
Cache plan (implemented, confirmed working): `docker-build-check.yml` uses
`cache-from: type=gha` (default/unscoped, matching `docker-publish.yml`'s
scope), no `cache-to` (read-only). `docker-publish.yml`'s first fully
successful run (30517766342) populated that shared cache.
Paths filter (PR #18, not yet merged): `Dockerfile`, `.dockerignore`,
`go.mod`, `go.sum`, `**/*.go`, plus each workflow's own file, added to both
workflows' triggers.

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-30: cache-scope alignment landed via PR #17 and verified working —
  docker-publish's first successful run populated the shared default gha
  scope, which docker-build-check now reads from.
- 2026-07-30: opened PR #18 for the paths-filter follow-up (not the human's
  original ask, but a direct consequence of it — noticed while watching CI
  during this session that non-code board commits were triggering full
  builds) — leaving card in now/ until merged and confirmed.

## Handoff notes
<!-- written by whichever role/session was last active on this card, before handing
     off or ending a session. What's half-done, what the next role should do first. -->
PR https://github.com/chrisjohnson/printer-dashboard/pull/18 is open, not
merged. Once merged, confirm a board-only commit to main doesn't trigger
`docker-publish.yml` (check the Actions tab / `gh run list`), then move this
card to done/ with that confirmation in the decision log.
