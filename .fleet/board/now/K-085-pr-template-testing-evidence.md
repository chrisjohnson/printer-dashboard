---
id: K-085
title: PR template requiring testing evidence
initiative_id: null
claimed_by: clever-fennec-reef
claimed_at: 2026-07-16T03:30Z
blocks: null
blocked_by: null
related_cards: []
---

# K-085 — PR template requiring testing evidence

## Context

Human-requested (2026-07-16): add a GitHub PR template to this repo that
requires every PR to demonstrate testing evidence for its changes before
it can be considered ready — not just a checkbox, but a template shape
that makes it easy to paste in commands run + output, or manual
verification steps + observed results, and awkward to skip.

No `.github/pull_request_template.md` (or `PULL_REQUEST_TEMPLATE/` dir)
currently exists in this repo — this is a net-new addition, not an edit.

## Plan
<!-- ordered checklist -->
1. [ ] Implementer: Add `.github/pull_request_template.md` with sections for: Summary (what/why), Test plan (explicit space for commands run + their output, or manual steps + observed results — not just a checklist of unchecked boxes), and a Testing evidence section requiring the PR author to state what was actually verified vs. what wasn't (mirroring the honesty bar this fleet already holds itself to on cards — see K-075's decision log for the tone/precedent: distinguish "verified by X" from "could not verify, here's why"). Keep it short enough that authors will actually fill it in rather than delete it.
2. [ ] Implementer: Confirm GitHub picks up the template automatically on new PRs (`.github/pull_request_template.md` is the standard single-template path — no repo config wiring needed, but sanity-check by opening `gh pr create` help / GitHub docs if unsure of exact filename casing/location).
3. [ ] Implementer: Commit the template. No app code changes, no tests to run beyond confirming the file is valid Markdown.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: clever-fennec-reef 2026-07-16T03:30Z — claiming, human-requested card -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->

## Handoff notes
<!-- what's half-done, what the next agent picking this up should do first. -->
