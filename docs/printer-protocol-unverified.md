# Unverified Items and Open Questions

Companion to [`printer-protocol-reference.md`](printer-protocol-reference.md).

The reference document contains only claims supported by code inspection or direct measurement.
Everything that could not be established to that standard lives here, with the specific test that would
resolve it. Nothing in this file should be treated as protocol fact.

Each entry lists what is suspected, why it is not confirmed, and the cheapest experiment that would
settle it.

---

## Bambu Lab

### Does the cloud-reported `dev_access_code` match the code shown on the printer LCD?

The cloud device-list endpoint returns `dev_access_code`, but local access (MQTT, RTSPS, camera) is
normally configured from the value printed on the printer's screen. Whether the two are always
identical has not been checked.

**Why it matters:** if they match, local access can be provisioned entirely from the cloud API with no
physical interaction with the printer.

**Test:** read `dev_access_code` from the device list and use it as the password for a LAN MQTT
connect and an RTSPS `DESCRIBE`. Success on both proves equivalence.

### RTSPS probing: the 301 and the 404 cannot both be observed

Direct probing of an H2-class unit with liveview enabled showed every path redirected:

Direct probing of an H2-class unit with the liveview toggle enabled showed:

```
DESCRIBE rtsp://<ip>:322/streaming/live/1 RTSP/1.0   → 301 Moved Permanently
                                                        Location: rtsps://<ip>/1
DESCRIBE rtsp://<ip>:322/1                RTSP/1.0   → 301 Moved Permanently
                                                        Location: rtsps://<ip>/1
```

Port 443 is closed, so the redirect target cannot be followed as written. A working stream was **not**
reproduced by hand during these checks, even though the documented URL form works when used by an
established RTSP client.

Separately, `/streaming/live/2` is recorded as returning **404 against a live unit**. A server that
answers every request with the same redirect makes a 404 unobservable, so at most one of these was
obtained through the same code path. The likely reading is that the 404 came from a proper RTSP client
and the 301 from a hand-rolled probe the server rejected before evaluating the path.

Neither observation is currently reproducible here: no RTSP client is available in this environment, and
the raw probe output was never recorded.

**Test:** capture the full exchange of a working RTSP client (`openssl s_client -connect <ip>:322`, or a
packet capture), then re-probe `/streaming/live/2` through that same client. Neither `ffmpeg` nor
`ffprobe` was available where these notes were taken.

### Does the H2 class report homing state through `home_flag` bit 0?

An H2-class unit reported `home_flag = 0xc0675590` with bit 0 clear, and after an **accepted** `G28`
the flag did not change within 60 seconds. Either the model does not use bit 0 the way P1-class
machines do, or homing state is reported through another field.

**Why it matters:** a homing indicator that never lights, or movement gates that stay disabled.

**Test:** capture `home_flag` before and after a successful `G28` on an H2-class machine, and compare
against the same sequence on a P1-class machine.

### What exactly produces `err_code 84033544`?

This code is contested between two causes: the file-verification stage (bad `md5`, plaintext `url`, or
an `ftp:///` scheme) and `sequence_id` reuse across printer restarts. Both have been observed in
different contexts and neither has been isolated.

**Test:** issue signed commands with a monotonically increasing `sequence_id` across a reboot and see
whether reuse alone reproduces the code.

### AMS unit identifiers above 3

Unit IDs 128–135 are documented for AMS-HT units but are not modelled anywhere. The arithmetic
`tray_now = (ams_id * 4) + tray_id` cannot represent them: IDs of 512 or above can never match a
`tray_now` value.

**Test:** connect an AMS-HT unit and capture the `id` values actually reported.

### `ams_exist_bits` and `tray_exist_bits`

Both fields are present in reports and are parsed by some clients, but their bit semantics are
described only as "may vary by firmware". They are unused and unverified.

**Test:** capture reports with zero, one, and two AMS units attached and diff the bit patterns.

### AMS control payloads

`ams_change_filament`, `unload_filament`, `ams_control`, and `ams_filament_setting` come from upstream
documentation. None have been confirmed against hardware, and all are `print`-namespace commands, so on
current firmware they require signing.

**Risk:** sending unrecognised `print` commands to older firmware may raise verification or
version-gate errors.

**Test:** issue each against a spare AMS-equipped machine with command acks enabled and record
`result` / `err_code`.

### AMS filament drying

Only the command name and an approximate 65 °C ceiling are recorded. The payload shape is unknown.

### Port 6000 concurrent connection limit

A limit was asserted in planning material and long treated as unverified. A live probe later observed
the port refusing a second client while one was streaming, which is consistent with a limit of one.
The exact limit is still unmeasured.

**Test:** with no other client attached, open N simultaneous TLS connections to port 6000 and find where
it fails.

### Local MQTT behavior with Developer Mode enabled

The documented local-MQTT parameters (username `bblp`, access-code password, same topics, plaintext
payloads, enforced auth) were established in earlier measurements against an H2-class unit running
firmware `ver=20008`. They were **not** re-measured during the current review pass, because enabling
Developer Mode requires physical interaction with the printer and would have taken that machine
offline from the cloud.

Treat this section as one confidence tier below the rest of the document.

**Test:** enable Developer Mode, then re-run the connection matrix and confirm the cloud connection
does in fact stop.

### Cloud token lifetime

Community reports put the lifetime at roughly three months. No token file has been observed long enough
to confirm this, and the stored expiry field is usually unusable (see the reference document).

### Firmware gate date

"Roughly January 2025" is the boundary inferred from the units available for testing. The precise
firmware version at which command verification became active is not known, and a specific firmware
version string should not be quoted as the cutoff.

---

## Snapmaker U1

### What is the TLS listener on port 8883?

Observed during a live probe (the probe output itself was not recorded as a repo artifact): the U1
accepts a TLS connection on port 8883 and terminates the handshake with
`TLSV13_ALERT_CERTIFICATE_REQUIRED`, meaning it demands a **client certificate**. It is not the
Moonraker API, and no client certificate is known to be available for it.

**Why it matters:** it may be a supported local control path, a vendor telemetry channel, or a
diagnostic interface.

**Test:** determine whether the Moonraker instance or a separate service owns the port, and whether any
locally available certificate is accepted.

### Filament system

The `FEED_AUTO` commands, the `filament_feed` and `filament_detect` object queries, and the state and
error enumerations are transcribed from documentation. None have been confirmed against a machine with
the automatic filament system installed.

### Does the WebSocket ever deliver status?

The subscribe-handshake requirement and the read-deadline-versus-ping-period interaction are derived
from code inspection and from Moonraker's documented protocol, not from a confirmed working session on
this hardware. Polling is used as the primary source, which means a working WebSocket path may never
have been exercised.

**Test:** send a correct `printer.objects.subscribe` JSON-RPC request and log the resulting
`notify_status_update` frames.

### Which call sites are exposed to the HTTP-200-with-error problem?

The embedded-error response is confirmed on the G-code path. Whether pause, resume, cancel, and the
object-query path can also return it has not been audited.

**Test:** stop Klipper and issue each endpoint, recording which return an embedded error with status
200.

---

## Environment-specific observations

These were measured on one network and one firmware configuration. They are recorded because they are
useful, not because they are universal.

- A P1-class unit with no local features enabled was recorded as exposing **only port 8883**. Read this
  record with care: an earlier transcription of the same observation carried the qualifier that port
  6000 was *held by another client's camera connection* at the time, and that qualifier was lost when
  the note was copied. So this is not a clean scan of an unoccupied port, and it tells us nothing about
  whether the liveview toggle gates 6000 on this class.
- On that same unit, local MQTT on 8883 answered every `CONNECT` with return code `2` (identifier
  rejected) regardless of client ID, username, or password — including with a correct access code. This
  is consistent with Developer Mode being off, but the specific rejection reason was not determined.
- On an H2-class unit with liveview enabled, local MQTT on 8883 completed the TLS handshake and then
  closed the connection without sending a `CONNACK`, for MQTT 3.1.1 and MQTT 5 alike. Again consistent
  with Developer Mode being off, but not proven to be that.
- MQTT protocol version is enforced: a level-5 `CONNECT` is refused with return code `1` while level 4
  proceeds. This was observed on one model only.

### Port 6000 single-session behavior — what is still unknown

Resolved: the camera service accepts one client and silently drops further SYNs while occupied, which
explains conflicting scan results. Still unproven:

- whether the limit is exactly **one** or a small pool (cannot be tested without dropping a live session);
- whether the drop is global or **per source IP** (probes from this host all share one NAT address);
- whether the listener ever closes when the printer is idle or after a reboot;
- whether the drop is occupancy or a **SYN rate limit** triggered by a client retrying hundreds of times;
- whether the liveview toggle has any effect on 6000 on P1-class at all;
- and the negative half: nobody has scanned a P1-class 6000 with **no** client attached, so "P1-class
  exposes 6000 with all LAN toggles off" remains inference from successful application-level connects,
  not a scan result.

### Was Developer Mode ever actually measured?

No. Every local-mode fact in the reference document was gathered either from the cloud path or from a
printer with the **liveview** toggle enabled. No measurement was taken with Developer Mode itself
switched on, because doing so detaches the printer from the cloud and requires physical interaction.

Specifically unconfirmed:
- whether the cloud connection definitively stops under the Developer Mode toggle (the equivalent
  detach is measured for LAN mode generally, but toggle naming varies by firmware);
- the upstream claim that Developer Mode makes the signature envelope optional;
- whether the local broker additionally requires liveview to be enabled.

### Is the firmware of a pre-gate P1-class unit actually old?

Every statement that a given P1-class machine "predates the gate" is **inference** from the fact that it
accepts unsigned commands. The firmware version string does not appear in that unit's cloud reports, so
its age has never been read directly.

### Does real firmware ever populate `camera.ipcam_url`?

Clients are written to prefer it when present, but only synthetic test values are known. Real
population is unconfirmed.

---

## Snapmaker U1 — additional

### Does the firmware enforce the access code?

Unconfirmed. The routinely exercised path is the unauthenticated one, and stock Moonraker uses
`?access_token=` / an `Authorization: Bearer` header rather than `access_code`. The WebSocket endpoint
sends no access code at all, so if enforcement exists the socket handshake fails first.

### Which endpoints can return HTTP 200 with an embedded error?

Confirmed on the G-code path only. Pause, resume, cancel and the object query have not been audited.
