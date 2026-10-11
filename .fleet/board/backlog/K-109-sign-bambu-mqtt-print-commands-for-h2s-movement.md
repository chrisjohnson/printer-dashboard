---
id: K-109
title: Sign Bambu MQTT print commands so H2S movement works without Developer Mode
initiative_id: null
claimed_by: null
claimed_at: null
blocks: null
blocked_by: null
related_cards: [K-108, K-106, K-092, K-095]
---

# K-109 — Sign Bambu MQTT print commands so H2S movement works without Developer Mode

## Context

All research is done and recorded in `.fleet/research-notes.md` §K-108 —
read that first; do not re-derive it. Short version: the H2S rejects every
`print`-namespace MQTT command (`err_code 84033543`) because 2024+ firmware
cryptographically verifies them. Proven on live hardware that adding an
RSA-SHA256 `header` envelope moves the error to `84033545` ("valid
signature, cert not in trust store"), so the mechanism is confirmed and
**Developer Mode is not required** — the user has explicitly ruled it out.

## Blocked on

**No longer blocked.** A working recipe was verified on the live H2S on
2026-10-10 and is recorded at the top of `.fleet/research-notes.md`
("K-109 — WORKING RECIPE"). `G28` home-all returned `err_code:0`.

The critical detail: **`payload_len` must be a JSON string, not a number.**
Numeric gives `84033547`, which is indistinguishable from a certificate
failure and cost hours.

No slicer PEMs from Bambu Studio are needed, and no memory dump: the publicly
extracted Bambu Connect cert/key pair works, installed into the printer's
trust store via `app_cert_install` alongside a self-minted CRL.

## Work

1. Vendor the Bambu Connect cert + key (public since Jan 2025) and load them
   from config rather than hardcoding.
2. On printer connect, `app_cert_install` the leaf + a CRL with issuer CN
   `GLOF3813734089.bambulab.com`; re-run after printer reboots (RAM-only).
3. Sign `print`-namespace frames in `publishCommand` per the recipe, with
   `payload_len` as a **string** and `sequence_id` a string >= 2000.
4. Leave `system` and `pushing` unsigned.
5. Map `84033543` → not signed, `84033545` → cert not installed,
   `84033546` → malformed header, `84033547` → signature/`payload_len` wrong.

## Verification

- `security.app_cert_list` returns our cert fingerprint after install.
- `POST /api/printers/{h2s}/home` returns 200 and the printer physically
  homes; `homed` flips `false → true` from the printer's own report (this is
  the same end-to-end check already verified on the P1S).
- A small jog is accepted and acked.
- Existing tests still pass: `go test ./... -race -count=1`.

## Notes

- The cert bundled in `schwarztim/bambu-mcp` does **not** work: expired
  2025-12-12 and not in the printer's trust store.
- If Bambu rotates credentials, expect `84033545` again; the fix is refreshed
  PEMs, not code.
- The P1S currently works unsigned because its firmware predates the gate.
  It will likely need this same path after a firmware update.
