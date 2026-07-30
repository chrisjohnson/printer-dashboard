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
1. [x] Implementer: Inspected card layout — `.card` is already a flex column, `.move-section` just had `margin-top: 4px`.
2. [x] Implementer: Changed `.move-section` `margin-top: 4px` → `margin-top: auto` to push jog pad + Home All button to card bottom.
3. [x] Implementer: Verified across card variants — `margin-top: auto` works for all cards regardless of content height (nozzles, chamber, camera, error banners, etc.).
4. [x] Implementer: Tested in container — change is live.

## Signals

## Decision log

## Handoff notes
