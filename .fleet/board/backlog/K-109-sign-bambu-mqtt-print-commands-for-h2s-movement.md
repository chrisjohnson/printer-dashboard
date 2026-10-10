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

Waiting on the user to copy `slicer_cert.pem` / `slicer_key.pem` /
`slicer_crl.pem` out of a machine that has run Bambu Studio (data dir is
`~/.config/BambuStudio/`, `~/Library/Application Support/BambuStudio/`, or
`%AppData%\BambuStudio\`). Until those land, nothing further can be verified.

## Work

1. Config: a credentials directory holding `slicer_{cert,key,crl}.pem`.
2. On printer connect, publish `security.app_cert_install` (fields
   `command` / `sequence_id` / `app_cert` = 3 PEMs / `crl` = 1 CRL) and
   confirm with `security.app_cert_list`. **The printer's trust store is in
   RAM and a reboot clears it**, so this must be re-run per session and after
   any printer restart.
3. Try it over the **existing cloud connection first**. Bambu's cloud is a
   plain forwarder (same topics, same JSON), so this may need no LAN support
   at all. Fall back to LAN (`bblp` + access code, command topic
   `device/{serial}/request`, plaintext) only if cloud fails.
4. In `publishCommand`, when a cert is loaded, wrap `print`-namespace
   payloads in the `header` envelope documented in K-108:
   - sign the byte-exact `JSON.stringify` of `{<ns>:{...}, user_id:"<uid>"}`
     — `user_id` is a top-level sibling and is covered by the signature
   - `cert_id` = 32-hex cert fingerprint + issuer CN
   - `payload_len` = UTF-8 byte length of the signed object
   - `sequence_id` must be a **string >= 2000** (fixed floor; below that you
     get `84033545` even with a trusted cert)
5. Leave `system` and `pushing` unsigned — they are not verified.
6. Surface the error family distinctly in the UI: `84033543` → "command not
   signed", `84033545` → "printer does not trust our certificate (re-install
   or refresh credentials)".

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
