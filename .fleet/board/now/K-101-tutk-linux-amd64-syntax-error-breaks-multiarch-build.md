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
1. [x] Research: read `internal/camera/tutk/tutk_linux_amd64.go` around line 309, compare against `tutk_linux_arm64.go` (or equivalent) to understand the intended code and root cause of the syntax error.
2. [x] Implementer: fix the syntax error; verify build.
3. [x] Implementer: push fix on a new branch, open PR vs main.
4. [ ] Human: merge PR #17 and re-run the docker-publish workflow to confirm the multi-arch image now builds and publishes successfully end-to-end.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: claude 2026-07-30T04:10:00Z — claiming, fixing amd64 build break surfaced by K-100 -->
<!-- signal: claude 2026-07-30T04:35:00Z — PR #16 open with fix, awaiting human merge + CI verification -->
<!-- signal: claude 2026-07-30T04:45:00Z — PR #16 merged but run 30514402320 hit a 2nd cgo error (missing unistd.h); PR #17 opened after a full-file audit, awaiting merge + CI verification -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->
Failing run: https://github.com/chrisjohnson/printer-dashboard/actions/runs/30512754706/job/90776113014
File: internal/camera/tutk/tutk_linux_amd64.go, line 309, col 13.
Root cause: `info.type` — `type` is a Go reserved keyword, cgo renames a
colliding C struct field to `_type` in the generated Go struct. Fix:
`info._type`. This is the *only* real TUTK implementation (filename-gated
`linux && amd64`); `tutk_stub.go` is the no-op fallback for every other
platform including arm64, so arm64 was never at risk from this bug.
Sandbox couldn't cross-compile/cgo-build linux/amd64 or run Docker (no
network egress) — fix is a one-line, high-confidence deterministic cgo rule,
but the real verification is the next Actions run after merge.

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-30: fix could not be locally verified via cross-compile or Docker
  build in the sandbox (no linux/amd64 cgo toolchain, no Docker network
  egress) — leaving card in now/ rather than done/ until a human merges
  PR #16 and confirms the Actions run succeeds; this is a real external
  verification gap, not a formality.
- 2026-07-30: PR #16 merged; next Actions run (30514402320) hit a *different*
  compile error at the same file — `C.usleep`/`C.useconds_t` undeclared,
  missing `#include <unistd.h>` in the cgo preamble. Confirms this file has
  never actually compiled for amd64 before, so more latent issues were
  plausible. Rather than fix-and-CI-retry one error at a time, dispatched a
  full manual audit of every `C.xxx` symbol against its required header and
  every Go-reserved-keyword struct selector in the file — PR #17 adds the
  missing include; audit found nothing else needing a fix.

## Handoff notes
<!-- written by whichever role/session was last active on this card, before handing
     off or ending a session. What's half-done, what the next role should do first. -->
PR https://github.com/chrisjohnson/printer-dashboard/pull/17 is open, not
merged (PR #16 already merged and is superseded/built upon by #17). Once #17
merges, watch the docker-publish workflow run on main; if it succeeds, move
this card to done/ with that confirmation noted in the decision log. If it
fails on a third distinct error, this file may need review by someone who
can actually compile it (Linux/amd64 machine or a sandbox with Docker
network egress) rather than another blind audit pass.
