---
id: K-105
# Filename pattern: {ID}-{slugified-title}.md
title: X1 series (BL-P001) camera routed to port-6000 P1/A1 protocol instead of RTSPS on 322
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: null                 # pet name of the agent session working this card, e.g. otter
claimed_at: null                 # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                 # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-105 — X1 series (BL-P001) camera routed to port-6000 P1/A1 protocol instead of RTSPS on 322

## Context
<!-- why this card exists: root cause, links to runbooks/PRs/related cards -->

GitHub issue #21 (https://github.com/chrisjohnson/printer-dashboard/issues/21)

On an X1 Carbon (model code `BL-P001`), the built-in camera never loads. The client routes X1-series printers to the port-6000 `bambus://` binary protocol, but the X1 series does not serve that protocol — it serves **RTSPS on port 322**.

### Root cause

`CameraStreams()` in `internal/printers/bambu/client.go:200` sends non-H2S models down the port-6000 path. `IsH2S()` (client.go:718) matches `H2S`, `H2D`, `H2C`, `H2D PRO`, `P2S`, `X2D`, `O1S` — so `BL-P001` falls through to the `else` branch and tries `bambus://` on port 6000.

The same split exists in the pre-connect loop at `internal/server/server.go:100-118`.

Result: tight reconnect loop — printer accepts TCP/TLS on 6000 but rejects the auth handshake with a non-JPEG error response.

### Confirmed working path

RTSPS on port 322 works:
```
ffprobe -rtsp_transport tcp "rtsps://bblp:<access-code>@<printer-ip>:322/streaming/live/1"
```
Returns h264 1168×720.

### Suggested fix

Route X1-series models to the RTSPS path. A `UsesRTSPS(model)` predicate covering both H2 series and X1 series would be cleaner than widening `IsH2S()`. Need to confirm which X1 model codes exist (BL-P001 for X1C, plus X1E and plain X1).

## Plan
<!-- ordered checklist -->
1. [ ] Audit all model codes the codebase knows about and identify which need RTSPS vs bambus://
2. [ ] Extract a `UsesRTSPS(model)` predicate from the `IsH2S()` split in `client.go` and `server.go`
3. [ ] Ensure `IsH2S()` semantics (HasChamber, multi-camera) are preserved for H2-only behavior
4. [ ] Verify the fix works for BL-P001 and doesn't regress existing model paths
5. [ ] Add tests covering both protocol paths

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->

## Handoff notes
<!-- what's half-done, what the next agent picking this up should do first. -->
