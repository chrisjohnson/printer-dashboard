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
1. [ ] Implementer: add `.github/workflows/docker-build-check.yml` (or a `pull_request` trigger + conditional push in the existing workflow — prefer a separate file for clarity) that builds `linux/amd64,linux/arm64` via buildx with `push: false`, triggered on `pull_request` targeting `main`, using GHA layer cache.
2. [ ] Implementer: commit on the existing `K-101-tutk-amd64-full-audit` branch (not a new branch) and push, so it lands on PR #17 and runs against it immediately.
3. [ ] Implementer/human: watch the new workflow's run on PR #17 to confirm the amd64 build now succeeds (or surfaces a 3rd distinct error).

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: claude 2026-07-30T05:05:00Z — claiming, adding PR-triggered build-only check per direct human request -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->
K-100's `docker-publish.yml` triggers on push to main/tags + workflow_dispatch,
no `pull_request` trigger (intentional, so untrusted PR code never gets
`secrets.GITHUB_TOKEN`/push access — see K-100 decision log). A build-only
`pull_request`-triggered workflow needs no registry login/secrets at all, so
it's safe to run on any PR including forks.

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->

## Handoff notes
<!-- written by whichever role/session was last active on this card, before handing
     off or ending a session. What's half-done, what the next role should do first. -->
