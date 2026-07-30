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
1. [ ] Research: confirm Dockerfile location/build context, existing CI workflows, image name conventions, and any prior art from K-084/K-088/K-089.
2. [ ] Implementer: create new branch off updated main; add `.github/workflows/docker-publish.yml` building and pushing to `ghcr.io/<owner>/<repo>` using `GITHUB_TOKEN`, tagging `latest` on default-branch pushes plus semver/sha tags; commit.
3. [ ] Implementer: push branch, open PR vs main with a description covering how to pull/run the published image from another machine.
4. [ ] Review: sanity-check workflow YAML (permissions: packages: write, correct registry/login action, tag strategy) and confirm PR is open.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: claude 2026-07-29T23:42:33Z — claiming, adding GHCR publish workflow per direct human request -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->

## Handoff notes
<!-- written by whichever role/session was last active on this card, before handing
     off or ending a session. What's half-done, what the next role should do first. -->
