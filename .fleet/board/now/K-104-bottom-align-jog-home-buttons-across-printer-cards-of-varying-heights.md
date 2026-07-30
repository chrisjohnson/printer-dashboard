---
id: K-104
title: Bottom-align jog/home buttons across printer cards of varying heights
initiative_id: null
claimed_by: null
claimed_at: null
blocks: null
blocked_by: null
related_cards: []
---

# K-104 — Bottom-align jog/home buttons across printer cards of varying heights

## Context

When multiple printer cards are displayed side-by-side in the dashboard grid, cards that have different heights (due to different numbers of nozzles, camera presence/absence, etc.) result in shorter cards having empty space at the bottom. The jog pad, Home All button, and step size selector are top-aligned within their move section, so shorter cards show a large gap between the last content (camera/error/temps) and the bottom of the card.

The user wants the jog pad, Home All button, and step size selector to be **bottom-aligned** within each card so that the move section sits flush at the bottom of every card, regardless of card height. This creates a consistent visual anchor at the bottom of each card.

## Plan
1. [ ] Implementer: Inspect current card layout in `moveSectionHtml()` (onboarding.go) and the card container CSS.
2. [ ] Implementer: Add CSS flexbox to the card container so the move section is pushed to the bottom (e.g., `flex-direction: column; min-height: ...;` with move-section having `margin-top: auto`).
3. [ ] Implementer: Verify alignment looks correct for all card variants (with/without camera, with/without chamber, with/without nozzle temps, with/without error banners, etc.).
4. [ ] Implementer: Test across different viewport widths.

## Signals

## Decision log

## Handoff notes
