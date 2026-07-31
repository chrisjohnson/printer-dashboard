---
id: K-105
title: X1 series (BL-P001) camera routed to port-6000 P1/A1 protocol instead of RTSPS on 322
initiative_id: null
claimed_by: pi
claimed_at: 2026-07-31T00:00Z
blocks: null
blocked_by: null
related_cards: []
---

# K-105 — DONE

## Decision log
2026-07-31 — Fixed by adding \`UsesRTSPS()\` predicate covering both H2 series and X1 series (BL-P001) for camera protocol routing; kept \`IsH2S()\` for H2-specific semantics (HasChamber). PR #23.
