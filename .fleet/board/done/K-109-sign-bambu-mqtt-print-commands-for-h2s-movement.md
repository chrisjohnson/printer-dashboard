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

## Problem

On the H2S every movement control was disabled or did nothing: Home All and the
jog buttons returned `err_code 84033543` (`mqtt message verify failed`). Bambu
firmware released after roughly January 2025 cryptographically verifies every
command in the `print` MQTT namespace, and the P1S is unaffected only because
its firmware predates that gate.

Developer Mode was explicitly ruled out. It is not required: it only makes the
signature envelope optional.

## Solution

`internal/printers/bambu/signing.go` builds the RSA-SHA256 `header` envelope the
firmware expects, and the client installs the signing certificate into the
printer's trust store on every connect.

Signing is adopted per printer by auto-detection: the first `print` command is
sent unsigned, and an `84033543` rejection flips `signRequired` and retries
signed. Printers whose firmware predates the gate never pay a rejected command,
so the P1S is behaviourally unchanged. `system` and `pushing` are not verified
and stay unsigned.

The certificate and key are Bambu's own shared app credentials, public since
January 2025. They are embedded in the binary and cached to the data directory
on first use so they can be inspected or replaced without a rebuild. See K-108
for the protocol record.

## Implementation notes

- `payload_len` must be a JSON **string**. A number is rejected with
  `84033547`, indistinguishable from a certificate failure.
- `cert_id` is the lowercase hex certificate serial plus `CN=` and the issuer
  common name, not a hash.
- `sequence_id` must be a string >= 2000 on signed frames; `sign()` injects one
  when the command builder omitted it.
- The signature covers the exact wire bytes minus `header`, so `sign()` splices
  the header on rather than re-marshalling.
- The printer's trust store is RAM-only and a reboot clears it, so
  `app_cert_install` runs on every connect rather than once at setup.
- `app_cert_install` requires a non-empty `crl` but does not validate its
  signature.
- Works over the existing cloud MQTT connection; no LAN transport was needed.
- `commandRejectedError` now carries the numeric `err_code` so callers branch on
  the verification family instead of matching message text.

## Verification

Live against H2S-001:

| action | before | after |
|---|---|---|
| `POST /api/printers/{id}/home` | `84033543` | accepted |
| `POST /api/printers/{id}/jog` | `84033543` | accepted |
| move pad buttons | disabled | enabled |

`go build`, `go vet`, and `go test -race -count=1 ./...` pass. New tests cover
the serial-based `cert_id`, byte-exact signature verification over the
header-stripped envelope, the `payload_len` string requirement, the
`sequence_id` floor, the `app_cert_install` shape, and `err_code` extraction.

## Follow-ups

- After an accepted `G28` the H2S `homed` flag did not flip within 60 s. Command
  acceptance is fixed; whether the H2S reports homing state through `home_flag`
  bit 0 the way the P1S does is unresolved and may need a parser change.
- The container cannot write `bambu_token.json` on the mounted volume (owned by
  the host user, container runs as `app`), so token refresh does not persist.
