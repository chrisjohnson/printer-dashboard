# H2S: signed MQTT commands without Developer Mode

**Supersedes the conclusion in `h2s-mqtt-command-verification.md`.** That file's
diagnosis (firmware verifies `print` commands cryptographically) is correct, but
its conclusion — "needs Developer Mode or Bambu's private key, no way forward" —
was wrong. A signing path exists and does not require Developer Mode.

## What was proven on the live H2S

Signing a `print` command with an RSA-SHA256 `header` envelope changed the
firmware's error code:

| command | result | err_code | meaning |
|---|---|---|---|
| unsigned `gcode_line` | failed | **84033543** | no signature present |
| signed `gcode_line` | failed | **84033545** | valid signature, `cert_id` not in trust store |

`84033545` is documented as *"need reset device pub key"* — i.e. the signature
verified cryptographically and was rejected only because the signing
certificate is not in the printer's trust store. Per the same source, **"any
installed leaf verifies"**, so installing a trusted cert is sufficient.

The scheme is therefore confirmed working end-to-end except for credentials.

## The wire format (verified against a working implementation)

```jsonc
// 1. build the payload, user_id as a top-level sibling
{ "print": { "sequence_id": "5000", "command": "gcode_line", "param": "G28\n" },
  "user_id": "1578637590" }

// 2. sign the EXACT bytes of JSON.stringify(payload) — no canonicalisation
sign_string = base64( RSA-SHA256( privateKey, payload ) )

// 3. append the header
{ ...payload, "header": {
    "sign_ver": "v1.0",
    "sign_alg": "RSA_SHA256",
    "sign_string": "<base64>",
    "cert_id": "<md5-of-cert><issuer CN>",
    "payload_len": <utf8 byte length of payload> } }
```

Gotchas that produce the same "verify failed" as a missing signature:

- `sequence_id` must be **>= 2000** on signed frames, or you get `84033545`
  even with a correctly installed cert. It is a fixed floor, not a high-water
  mark. Studio uses a 20000–29999 window.
- The signature covers the whole envelope minus `header`, including the trailing
  `user_id` sibling. Signing only the inner `{"print": ...}` is a known
  reimplementation bug.
- No JSON canonicalisation: the printer re-checks the byte-exact wire bytes, so
  the string you sign must be the string you publish.
- `cert_id` = 32-hex fingerprint + issuer CN, e.g.
  `9bed8c27b4bf69582d58f11abaaad99fCN=GLOF3813734089.bambulab.com`.

## Trust store on this printer

`security.app_cert_list` (must include `"type":"app"` and a **string**
`sequence_id`) answered `SUCCESS` with two trusted certs:

```
95554d66b3d9912fdcad7ea8e1b8fd15CN=GLOF3813734089.bambulab.com
9bed8c27b4bf69582d58f11abaaad99fCN=GLOF3813734089.bambulab.com
```

`9bed8c27…` also appears in another project's capture from a different printer,
so these are Bambu's **shared app certs**, not per-printer.

Trust store facts:
- Installed via `security.app_cert_install` (`app_cert` = 3 PEMs, `crl` = 1 CRL).
  Install **adds**, does not replace.
- **The store lives in printer RAM — a reboot clears it.** Re-install each
  session. Right after reboot `app_cert_list` fails with
  `reason: "device-endreaddevcertfailed"`.

## What is still missing

A private key whose certificate is in that trust store. Two ways to get one:

**Route 1 — copy the credentials Bambu Studio already fetched (easiest).**
Bambu Studio downloads the network plugin and the cert endpoint hands it the
shared app cert, CRL and private key; the fetcher in
`open-bamboo-networking/tools/fetch_slicer_credentials.py` compares against
`~/.config/BambuStudio/slicer_key.pem`, i.e. Studio keeps them locally. Running
Bambu Studio once on any machine should yield `slicer_cert.pem` /
`slicer_key.pem` / `slicer_crl.pem`, which can be dropped into our config dir.
This uses Bambu's own credentials for Bambu's own printer, on the owner's
network.

**Route 2 — fetch them directly.**
`GET /v1/iot-service/api/user/applications/{enc_secret}/cert?aes256=…&ver=1`,
where the request proves possession of `client_auth_secret` (43-byte string
embedded in the stock plugin) and `aes256` wraps a session key with
`server_wrap_key` (RSA-2048 public PEM, also embedded). The response `key` blob
is opened with a non-standard AES-256-CTR using a **custom S-box** and fixed key
`00..1f`, CTR starting at 2. OBN ships this as
`tools/fetch_slicer_credentials.py`; the two bootstrap secrets must be extracted
from the stock plugin (`tools/extract_key_from_dump.py`, method in
`research/10.07-bearssl-crypto-island.md`). They are deliberately not published.

**Not a route:** the cert bundled in `schwarztim/bambu-mcp`. It is a real
Bambu Connect cert but from Dec 2024, expired 2025-12-12, and its MD5
(`c0e88e3e79e826f0fbf3187bc2077952`) matches neither trusted cert — which is
exactly why it produced `84033545`. Verified: cert/key are a valid pair
(matching modulus), so the failure is trust, not a broken key.

## Implementation sketch for this app

1. Config: per-printer `access_code` (already present) + a credentials dir for
   `slicer_{cert,key,crl}.pem`.
2. On connect (LAN): `app_cert_install` the chain, then verify with
   `app_cert_list`. Re-run after printer reboots.
3. In `publishCommand`, when a cert is loaded, wrap the payload in the `header`
   envelope above with `sequence_id` from a counter starting well above 2000.
4. Keep unsigned for `system` / `pushing` (they are not verified).
5. Error mapping: `84033543` → "not signed", `84033545` → "cert not trusted by
   printer (re-install or update credentials)".

Depends on LAN MQTT (K-106) for the transport, since the cloud path also
forwards these frames but the printer's trust store is per-session.

## Corrections

- "Requires Developer Mode" — wrong. Developer Mode merely makes the `header`
  envelope optional.
- "The only alternative is Bambu's proprietary key, unobtainable" — half wrong.
  The shared app key is obtainable: Bambu's cert endpoint serves it to any client
  that can prove it shipped with the official bootstrap secret, and Bambu Studio
  already caches it locally.
