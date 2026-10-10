# H2S "MQTT Command verification failed" — investigation snapshot

Status: **superseded — see `h2s-mqtt-signing-path.md`.** The diagnosis below
(firmware cryptographically verifies `print` commands) is correct and was
measured, but the conclusion that this needs Developer Mode is wrong: signed
commands work without it. Kept for the measurement record and the refuted
hypotheses, which still stand. Written as a hand-off snapshot so a fresh session
does not re-derive it. Every claim below was measured on the live fleet, not
inferred.

Fleet at time of writing: P1S-001 `01P09C4B1600631` @ 192.168.1.15 (access code
`64894711`), H2S-001 `0938AC580200372` @ 192.168.1.17 (access code `d5a78d50`),
U1 (Snapmaker). Bambu cloud account `u_1578637590`, region global.

## Symptom

On the H2S every movement / temperature command is refused. The dashboard used
to show nothing at all because the app discarded the firmware's reply and
returned HTTP 200 (fixed in PR #34 — it now surfaces the reason). The printer
also raises HMS `0500-0500-0001-0007`, which puts it in `state: "error"`.

The firmware's reply, verbatim:

```json
{"print":{"command":"gcode_line","err_code":84033543,"is_from_mqtt":true,
 "param":"G28\n","reason":"mqtt message verify failed","result":"failed"}}
```

## Root cause

Recent (2024+) Bambu firmware **cryptographically verifies every MQTT `print`
command** while the printer is paired to the Bambu cloud. Signing requires a
private key that ships obfuscated inside Bambu's closed-source network plugin.
This app does not have that key, so it cannot produce a signature the printer
will accept.

Documented independently by
[ClusterM/open-bamboo-networking](https://github.com/ClusterM/open-bamboo-networking),
an open-source reverse-engineering of the stock plugin, which describes this
exact failure:

> If you run **neither** Developer Mode **nor** valid credentials, the printer
> rejects every `print` / `project_file` command and shows on its screen:
> `MQTT Command verification failed` / `err_code: 84033543`

Bambu's own [third-party integration wiki](https://wiki.bambulab.com/en/software/third-party-integration)
lists "controlling printer functions such as movement, temperature, fans, AMS
settings, and calibration" as requiring authorization in **both** cloud and
normal LAN mode, and explicitly exempts status pushes — which matches the
measured split exactly (telemetry and the chamber light work; `print` does not).

The P1S accepts the same `print` commands from the same code and account, so
its firmware predates the gate. **This is an inference, not a measurement**: the
P1S never includes `ver` in its cloud reports and its LAN broker streams
nothing, so its firmware version could not be read. Expect the P1S to start
failing the same way after a firmware update.

## Measured behaviour

| namespace | via cloud | via LAN (`bblp` + access code) |
|---|---|---|
| `pushing.pushall` | success, full report | accepted |
| `system.ledctrl` | success, light physically toggles | **success, light physically toggles** |
| `print.gcode_line` | `failed` / verify failed / 84033543 | **identical failure** |
| status reports | sparse; P1 deltas omit `gcode_state` | **plaintext JSON, ~1 report/sec** |

Identical failure on both transports is what rules out transport, encoding,
topic, and payload shape.

## LAN facts established (needed if LAN MQTT is ever implemented, K-106)

- Broker: `mqtts://<printer-ip>:8883`, TLS with a self-signed cert
  (`rejectUnauthorized: false`).
- Auth: username `bblp`, password = access code. **Enforced** — a wrong password
  gets `Connection refused: Not authorized`, which also confirms the stored
  access code is valid.
- Command topic: **`device/{serial}/request`**. `device/{serial}/mqtt` is
  silently ignored on this firmware.
- Payloads are **plaintext JSON** on this firmware — reports *and* commands. No
  AES envelope is involved. Sending a ciphertext envelope to `request` is
  silently dropped; plaintext with a wrong shape gets an explicit
  `result:"failed", reason:"json wrong format"`.
- Command acks arrive on `device/{serial}/report`.
- Reports arrive ~1/sec, far better than the cloud path.
- P1S LAN: TCP 8883 accepts a connection but publishes **no** reports (LAN mode
  off), so LAN is not currently usable for the P1S.
- Open ports — H2S: 3000 (FTP), 6000 (camera), 8883 (MQTT). P1S: 8883 only
  (6000 is held by this app's camera connection). **No HTTP control API on
  either printer**, so there is no local Bambu-Connect endpoint to target.

## Hypotheses tested and refuted

Each was falsified by experiment, not reasoning. Do not re-litigate without new
evidence.

1. **Missing / wrong `sequence_id`.** 11 payload variants over LAN, all with
   instant acks: absent, string `"1"`, number `1`, string `"0"`, with `id`,
   with `mc_action`, with `account`, with `version`, no trailing newline,
   `M115` instead of `M105`, trailing space. **All** returned
   `verify failed / 84033543`.
2. **Encrypted `bblp=` envelope.** Invalid on two counts: encryption is not used
   by this firmware at all (LAN reports and commands are plaintext), and the
   first test sent ciphertext to the *cloud* broker.
3. **Wrong LAN topic.** `request` vs `mqtt` — both tried; `request` is correct
   and does execute `system` commands.
4. **Wrong payload shape.** The printer says `json wrong format` when the shape
   is wrong, which is how the correct `ledctrl` shape was found. `gcode_line`
   never produced that error, so its shape was always fine.
5. **Printer bound to a different Bambu account.** Could not be checked via the
   cloud API — every `api.bambulab.com/v1/...` device-list endpoint returns 404
   with the stored token. Counterevidence: `system.ledctrl` from the same
   identity physically actuates the light, so the identity is accepted for that
   namespace.
6. **Stale access code.** Refuted: wrong passwords are refused, the stored one
   authenticates.

## Corrections to earlier conclusions in this investigation

Both were wrong in ways worth remembering:

- Claiming LAN "ignores all commands" was **our bug**: the probe used invented
  `ledctrl` keys (`led_on`, `cycle_times`) instead of the real
  `led_node`/`led_mode`/`led_on_time`/`led_off_time`/`loop_times`/`interval_time`
  shape. The printer was replying `json wrong format` the whole time.
- Claiming the encrypted envelope was "not the unlock" was based on testing it
  against the wrong broker and the wrong topic.

## Options, none of which is a code fix in this app

1. **Developer Mode on the printer** (Settings → LAN Only → Developer Mode).
   Turns verification off; third-party commands then work over LAN. Cost: the
   printer leaves the Bambu cloud, so this app — which is cloud-only today —
   would lose it entirely unless LAN MQTT (K-106) is implemented.
2. **Bambu Local Server SDK.** The official third-party route; requires applying
   for access. Capabilities and latency for this use case are unknown.
3. **Bambu Connect.** Officially supports axis movement, but has no Linux build
   yet ("Linux: currently in development"), so a Linux dashboard cannot embed it.
4. **Bambu's private slicer credentials** (`slicer_cert.pem` / `slicer_key.pem`).
   Would work, but they are proprietary and not legitimately obtainable; the
   open-source project explicitly refuses to distribute them or say how to get
   them. Not a path for this project.

If the goal is "movement works on the H2S", the only realistic route is
**Developer Mode + implementing LAN MQTT**, ideally as a hybrid that prefers LAN
and falls back to cloud so the P1S and any non-LAN printer keep working.

## Live-state notes

- The H2S is currently `state: "error"` because of HMS `0500-0500-0001-0007`,
  raised by the failed commands. The app's dismiss endpoint only suppresses
  notifications, not the printer's reported state — a printer restart clears it.
- The H2S `home_flag = 0xc0675590` (bit 0 clear) so it is genuinely unhomed;
  every Z jog therefore opens the "Printer not homed" prompt, which then cannot
  be satisfied — that loop is downstream of this gate.
- The P1S is `complete` and fully controllable (home and jog verified working,
  `homed` flips from the printer's own report).
- Both chamber lights were left on, as found.

## Reproducing the probes

Needs `npm install mqtt` (if the npm cache is root-owned, use
`npm install mqtt --cache /tmp/npmcache`).

```js
// LAN: connect, subscribe, and send a correctly-shaped ledctrl.
import mqtt from 'mqtt';
const IP='192.168.1.17', SERIAL='0938AC580200372', ACCESS='d5a78d50';
const c = mqtt.connect({protocol:'mqtts', host:IP, port:8883, rejectUnauthorized:false,
  username:'bblp', password:ACCESS});
c.on('connect', () => {
  c.subscribe(`device/${SERIAL}/report`, {qos:0});
  c.publish(`device/${SERIAL}/request`, JSON.stringify({system:{
    sequence_id:'0', command:'ledctrl', led_node:'chamber_light', led_mode:'off',
    led_on_time:500, led_off_time:500, loop_times:0, interval_time:0}}), {qos:0});
  // swap to {print:{command:'gcode_line', param:'M105\n'}} to see the rejection.
});
c.on('message', (_t,p) => console.log(p.toString('utf8').slice(0,300)));
```
