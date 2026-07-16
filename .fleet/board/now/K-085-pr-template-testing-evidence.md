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
1. [x] Implementer: Add `.github/pull_request_template.md` with sections for: Summary (what/why), Test plan (explicit space for commands run + their output, or manual steps + observed results — not just a checklist of unchecked boxes), and a Testing evidence section requiring the PR author to state what was actually verified vs. what wasn't (mirroring the honesty bar this fleet already holds itself to on cards — see K-075's decision log for the tone/precedent: distinguish "verified by X" from "could not verify, here's why"). Keep it short enough that authors will actually fill it in rather than delete it.
2. [x] Implementer: Confirm GitHub picks up the template automatically on new PRs (`.github/pull_request_template.md` is the standard single-template path — no repo config wiring needed, but sanity-check by opening `gh pr create` help / GitHub docs if unsure of exact filename casing/location).
3. [x] Implementer: Commit the template. No app code changes, no tests to run beyond confirming the file is valid Markdown.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: clever-fennec-reef 2026-07-16T03:30Z — claiming, human-requested card -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-16 — clever-fennec-reef: added `.github/pull_request_template.md`
  (lowercase — GitHub's default-template matching is case-insensitive for
  this filename, no other config needed). Structured as Summary / Test plan
  (command:/result: prompts, not a bare checklist) / Testing evidence
  honesty (verified vs. could-not-verify-and-why), mirroring the honesty
  convention already established on this board (see K-075's decision log).
  Commit `22291fa` on `worktree-clever-fennec-reef`. Note: `AGENTS.md` was
  updated mid-card (§3b step 6) to require using a repo's PR template
  verbatim when opening PRs going forward — this card's own PR predates the
  template landing on main, so it's written by hand mirroring the new
  template's structure; every future PR from this repo should use the
  template automatically once merged.

## Handoff notes
Card complete. No separate PR — this branch (`worktree-clever-fennec-reef`)
was still shared with K-075's in-flight PR, so the template commit
(`22291fa`) landed in the existing https://github.com/chrisjohnson/printer-dashboard/pull/2
rather than a new PR. Per human follow-up request, PR #2's body was also
rewritten by hand to comply with the new template (Summary / Test plan with
command:/result: entries / Testing evidence honesty) — first real usage of
the template this card added.
