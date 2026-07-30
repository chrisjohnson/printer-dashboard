---
id: K-101
title: tutk_linux_amd64.go syntax error breaks multi-arch Docker build
initiative_id: null
claimed_by: claude
claimed_at: 2026-07-30T04:10:00Z
blocks: null
blocked_by: null
related_cards: [K-100]
---

# K-101 — tutk_linux_amd64.go syntax error breaks multi-arch Docker build

## Context

First real run of the K-100 GHCR publish workflow
(https://github.com/chrisjohnson/printer-dashboard/actions/runs/30512754706)
failed on `main` after PR #14 merged. The workflow/QEMU/buildx/GHCR login steps
all succeeded — the failure is a genuine Go compile error surfaced by building
for `linux/amd64` for the first time ever (this repo's Dockerfile previously
hardcoded an arm64-only go2rtc binary, so nobody had built/tested amd64 before):

```
# github.com/chrisjohnson/printer-dashboard/internal/camera/tutk
internal/camera/tutk/tutk_linux_amd64.go:309:13: expected selector or type assertion, found 'type'
```

This is a pre-existing latent bug in the amd64-specific build-tagged file,
unmasked by K-100's multi-arch change, not a workflow/CI config bug.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [ ] Research: read `internal/camera/tutk/tutk_linux_amd64.go` around line 309, compare against `tutk_linux_arm64.go` (or equivalent) to understand the intended code and root cause of the syntax error.
2. [ ] Implementer: fix the syntax error; run `go build ./...` / `go vet ./...` for amd64 and arm64 (`GOARCH=amd64 go build ./...`, `GOARCH=arm64 go build ./...`) locally to confirm both compile.
3. [ ] Implementer: push fix on a new branch, open PR vs main.
4. [ ] Implementer/human: re-run the docker-publish workflow (push or workflow_dispatch) to confirm the multi-arch image now builds and publishes successfully end-to-end.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: claude 2026-07-30T04:10:00Z — claiming, fixing amd64 build break surfaced by K-100 -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->
Failing run: https://github.com/chrisjohnson/printer-dashboard/actions/runs/30512754706/job/90776113014
File: internal/camera/tutk/tutk_linux_amd64.go, line 309, col 13.
Build was cancelled mid-way on the arm64 leg once amd64 failed, so arm64
compile status for this file is unverified too — check both.

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->

## Handoff notes
<!-- written by whichever role/session was last active on this card, before handing
     off or ending a session. What's half-done, what the next role should do first. -->
