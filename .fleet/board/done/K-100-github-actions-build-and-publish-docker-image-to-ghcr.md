---
id: K-100
title: GitHub Actions workflow to build and publish Docker image to GHCR
initiative_id: null
claimed_by: claude
claimed_at: 2026-07-29T23:42:33Z
blocks: null
blocked_by: null
related_cards: [K-012, K-084, K-088, K-089]
---

# K-100 — GitHub Actions workflow to build and publish Docker image to GHCR

## Context

Human request: add a GitHub Actions workflow that automatically builds and
publishes the Docker image to GitHub Container Registry (ghcr.io) on push,
so the image can be pulled from another machine to run a stable instance.
New branch off main, PR opened and linked back to the human when done.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [x] Research: confirm Dockerfile location/build context, existing CI workflows, image name conventions, and any prior art from K-084/K-088/K-089.
2. [x] Implementer: create new branch off updated main; add `.github/workflows/docker-publish.yml` building and pushing to `ghcr.io/<owner>/<repo>` using `GITHUB_TOKEN`, tagging `latest` on default-branch pushes plus semver/sha tags; commit.
3. [x] Implementer: push branch, open PR vs main with a description covering how to pull/run the published image from another machine.
4. [x] Review: sanity-check workflow YAML (permissions: packages: write, correct registry/login action, tag strategy) and confirm PR is open.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: claude 2026-07-29T23:42:33Z — claiming, adding GHCR publish workflow per direct human request -->
<!-- signal: claude 2026-07-30T00:05:00Z — PR #14 open, review clean, moving to done -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->
Image ref: `ghcr.io/chrisjohnson/printer-dashboard`. Multi-arch (amd64+arm64) per
explicit human choice — this required fixing the Dockerfile's `go2rtc` stage, which
previously hardcoded a `go2rtc_linux_arm64` download unconditionally. GHCR packages
are private by default; README now documents making the package public or `docker
login ghcr.io` with a `read:packages` PAT on the consuming machine.

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-29: research surfaced that the Dockerfile's go2rtc stage was arm64-only;
  asked the human whether to target amd64, arm64, or multi-arch rather than guessing —
  chose multi-arch (amd64+arm64) via buildx/QEMU, requiring a Dockerfile fix
  (`ARG TARGETARCH` in the go2rtc stage) alongside the new workflow.
- 2026-07-29: PR #14 opened against main; review agent found no correctness issues
  (permissions, login, trigger safety against untrusted PRs, tag strategy, and the
  TARGETARCH placement all check out) — moving card to done/.

## Handoff notes
<!-- written by whichever role/session was last active on this card, before handing
     off or ending a session. What's half-done, what the next role should do first. -->
PR https://github.com/chrisjohnson/printer-dashboard/pull/14 is open, not yet merged
— that's the human's call. Local sandbox couldn't run a full `docker build` (network
restrictions on base-image pulls), so the first real GitHub Actions run is the true
verification; watch it after merge. Nothing else pending on this card.
