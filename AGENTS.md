# AGENTS.md — printer-dashboard

A single-binary web dashboard for a fleet of 3D printers (Bambu Lab over MQTT,
Snapmaker over Moonraker HTTP/WS). One container, no compose, no database.

An earlier `AGENTS.md` was renamed to `AGENTS-bak.md` with no content change and
no stated reason; its printer-dashboard-specific guidance (container naming,
random host ports, shared token cache, single-container design) is folded in
below and the file has been removed. What it predates: the `--user 0:0`
requirement, the camera rule, and transport selection.


## Run it with cameras disabled unless you specifically need video

**Default every dev/test instance to `DISABLE_CAMERAS=1`.** Only the production
instance should have cameras enabled.

This is not a performance preference. Bambu camera ports are **single-session**:
the P1S accepts exactly one client on `:6000`, and while a session is held it
**silently drops** further connection attempts — no SYN-ACK, no RST. The client
sees an `i/o timeout`, which is indistinguishable from "liveview is disabled on
the printer" or "the printer is off". Every dashboard instance eagerly
pre-connects its cameras at startup, so:

- whichever instance boots first **wins the camera**;
- every other instance shows a permanent, misleading "camera stream not active —
  check printer power/LAN mode" for a printer that is fine;
- the lock is only released when the holding process dies, so a stale container
  from an abandoned worktree can take the camera down across a restart.

This has bitten in production: a three-week-old container from a deleted worktree
was holding the P1S camera, and the liveview looked broken.

Set it whenever you are **viewing, testing, or troubleshooting camera feeds** —
and only then. Everything else (status, temperatures, AMS, HMS, controls,
notifications) is unaffected by the flag. See the run block under "Container
hygiene" for where it goes.

When disabled, the instance opens no camera connection, advertises no camera
streams, and cards render without a camera block at all.


## Container hygiene: name per checkout, random host port, always clean up

This project gets built and run by more than one checkout at a time, so a fixed
image name, container name and host port will eventually collide. Derive the name
from the checkout and publish to a random port:

```bash
NAME="printer-dashboard${WORKTREE:+-$WORKTREE}"
docker build -t "$NAME" .

docker rm -f "$NAME" || true      # never let a leftover from a prior run block you
docker run -d --name "$NAME" \
  --user 0:0 \
  -e DISABLE_CAMERAS=1 \
  -p 0:8080 \
  -v "${HOME}/.printer-dashboard:/home/app/.printer-dashboard:rw" \
  -v "${HOME}/.printer-dashboard/config.yaml:/app/config.yaml:rw" \
  "$NAME"

docker port "$NAME" 8080          # the assigned host port
```

The `docker rm -f` line is not just about port conflicts. **A stopped-but-not-removed
container can keep holding printer resources**, and because the camera port is
single-session and the P1S local MQTT broker caps at roughly four clients, a
forgotten container degrades printers for every other instance — with errors that
blame the printer. Remove containers when you are done with them; do not leave
them stopped in the store.

This is a single-container workflow by design: no docker-compose, no sidecar
services. go2rtc is embedded in the binary.


## `--user 0:0` is required

The image builds with `USER app` (uid 1000), but `config.yaml` is a bind mount
that keeps its **host** ownership through the uid mapping. The container user is
therefore not the file's owner and cannot write it — `:rw` on the mount governs
the mount, not the file. Without the flag, the onboarding wizard's final step
fails and a completed Bambu sign-in (including 2FA) is silently discarded.

Under rootless Podman, container uid 0 *is* the host user, so this grants no
extra privilege. Full explanation in README → "Why `--user 0:0` is required".

## `config.yaml` and the token cache are shared machine-wide

Every worktree's container mounts the **same** `~/.printer-dashboard/` and the
same `config.yaml`. That is deliberate — the config describes the physical
printers, not a checkout — but it means:

- **a dev instance mutating config or credentials mutates production.** Writing
  config from a test instance rewrites the live printer list.
- Bambu **tokens are single-use per login**: signing in on a dev instance can
  revoke the token the production instance is using. If you need to test the
  auth flow, copy the token store and point your mount at the copy.
- Files the container creates in the token cache are owned by whichever uid it
  runs as. Switching between `--user 0:0` and the image default can strand the
  cached `bambu_app_*.pem` signing credentials; delete those three files to
  recover.

The sharing exists *because* of the single-use tokens: the cache is small, written
only at login, and tied to the cloud account rather than to a checkout, so one
shared cache means one login reused everywhere instead of each checkout
re-authenticating and revoking the last. That is the tradeoff — convenience in
exchange for the fact that a careless instance can log the fleet out.

## Runtime shape

No required environment variables. The binary takes an optional config path as
its first argument (default `config.yaml`, mounted at `/app/config.yaml`) and
listens on `:8080` inside the container. `DISABLE_CAMERAS` is the only optional
knob; see the camera section.


## Bambu transport: LAN first, cloud fallback

Telemetry and commands use the printer's **local** MQTT broker when a host and
access code are configured, falling back to Bambu cloud MQTT. Selection is
probed per printer at connect time, not looked up from a firmware or model
table — toggle naming varies across firmware generations and an open local port
does not mean the broker accepts third-party clients.

Consequences worth knowing:

- A dead cloud token no longer means a blind dashboard; it means a
  `cloud_degraded` notice on printers still streaming over LAN.
- Command signing is seeded from `bambu_account.user_id` in config, **not** from
  a successful cloud login, so local commands keep working when the token dies.
- Measured on this fleet: P1S local MQTT caps at roughly **4 concurrent
  sessions**; the H2S and the cloud broker showed no cap at 6 and 5. Keep
  concurrent instances low even with cameras off.
- Pin per printer with `mqtt_transport: lan | cloud`.

Design, rejected alternatives, and measurements: `docs/transport-selection-spec.md`.
Protocol details (ports, auth behaviour, signing gate, single-session camera):
`docs/printer-protocol-reference.md` — it is unusually good, read it before
theorising about printer behaviour.

## Building and testing

Go 1.26. Build and test in a container if the host has no toolchain:

```bash
go build ./... && go vet ./...
go test ./... -count=1
go test ./... -race -count=1     # always before committing
```

The frontend is an embedded Go string in `internal/server/onboarding.go` (~3,900
lines), not a separate JS bundle. There is no build step between editing it and
what a browser receives, so syntax errors in that string only surface at runtime.
`node --check` on the extracted `<script>` block is a cheap guard.

Playwright specs live in `tests/dashboard.test.ts` and need a running server
(`SERVER_URL=... npx playwright test`). They drive synthetic printer payloads
through the real `renderCard`/`updateCard` paths, so frontend changes should add
a case there.
