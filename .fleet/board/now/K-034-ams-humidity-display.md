---
id: K-034
# Filename pattern: {ID}-{slugified-title}.md
title: AMS humidity display
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: null                 # pet name of the agent session working this card, e.g. otter
claimed_at: null                 # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-034 — AMS humidity display

## Context

AMS humidity display. Focuses specifically on visualizing the humidity and temperature
sensors available on H2S AMS 2 Pro units.

**DEPENDENCY:** K-033 must be completed first - humidity is part of the AMS data structure
that needs to be parsed and exposed before it can be displayed. K-033 backend is complete
and awaiting review.

**Hardware note:** P1S AMS 1 has NO humidity/temp sensors. Only H2S AMS 2 Pro (and X1/X1C
with AMS) report these values. The `humidity` field is an index 0-5 (lower=drier), and
`humidity_raw` is the percentage. `temp` is the AMS internal temperature in Celsius.

See research:
- `.fleet/research-notes.md` K-055 section
- OpenBambuAPI MQTT docs: ams[].humidity, ams[].temp fields

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->

### Prerequisite
1. [ ] K-033 (AMS status display) must be completed first

### Frontend (Dashboard) - Humidity-specific UI
2. [ ] Add humidity indicator to AMS unit tiles (after K-033 base is done)
3. [ ] Convert humidity index (0-5) to visual indicator (e.g., color-coded: green=dry, red=wet)
4. [ ] Show raw humidity % tooltip or secondary display
5. [ ] Display AMS unit temperature if available
6. [ ] Handle missing humidity data gracefully (P1S, older firmware)

### Verification
7. [ ] Test with H2S hardware or mocked data with humidity fields
8. [ ] Verify P1S compatibility (no humidity crash)
9. [ ] Add signals and move to done/

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->

## Handoff notes
<!-- written by whichever role/session was last active on this card, before handing
     off or ending a session. What's half-done, what the next role should do first. -->
