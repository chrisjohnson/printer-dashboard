# AGENTS.md — printer-dashboard

A single-binary web dashboard for a fleet of 3D printers (Bambu Lab over MQTT,
Snapmaker over Moonraker HTTP/WS). One container, no compose, no database.

An earlier `AGENTS.md` was renamed to `AGENTS-bak.md` with no content change and
no stated reason; its printer-dashboard-specific guidance (naming containers per
checkout, the shared token cache and why, single-container design) is folded in
below and the file has been removed. Its random-host-port advice is deliberately
superseded — see "Port 8080 is the Caddy-visible default". What it also predates:
the `--user 0:0` requirement, the camera rule, and transport selection.


## Every instance on this machine is a dev instance — default to `DISABLE_CAMERAS=1`

Production runs on a **separate machine**, pulling the published GHCR image
(`ghcr.io/chrisjohnson/printer-dashboard:latest`, built on every push to `main`).
Nothing on this box is production. So there is no local instance that needs an
exception: **always pass `DISABLE_CAMERAS=1` here**, and drop it only for the
session in which you are actually looking at video.

This is not a performance preference. Bambu camera ports are **single-session**:
the P1S accepts exactly one client on `:6000`, and while a session is held it
**silently drops** further connection attempts — no SYN-ACK, no RST. The client
sees an `i/o timeout`, indistinguishable from "liveview is disabled" or "the
printer is off". Every dashboard instance eagerly pre-connects its cameras at
startup, so:

- whichever instance connects first **wins the camera**;
- every other instance shows a permanent, misleading "camera stream not active —
  check printer power/LAN mode" for a printer that is fine;
- the lock is only released when the holding process dies, so a forgotten
  container can take a camera down across restarts.

This has bitten twice: a three-week-old container from a deleted worktree was
holding the P1S camera and the liveview looked broken, and the error message
blamed the printer the entire time.

Note the cross-machine consequence: if the production host is on the same LAN as
the printers, it now prefers local MQTT too (see transport selection below), so a
dev instance here competes with **production** for the printer's single camera
session and its small local MQTT budget. Leaving cameras on in a dev instance is
not a self-contained choice.

When disabled, the instance opens no camera connection, advertises no camera
streams, and renders cards without a camera block at all.

## Building and running: use podman, not docker

This machine has no usable Docker for project work — you are not in the `docker`
group, and rootless **Podman** owned by the `dsh` account is the sanctioned
runtime. It is a different daemon and store: `sudo docker ps` will never list
these containers. Export the socket once per session and it works even under a
sandboxed session (plain `podman` without `-H` fails when `NoNewPrivs` is set):

```bash
export CONTAINER_HOST=unix:///run/user/1002/podman/podman.sock
podman ps        # your containers only

NAME="printer-dashboard-$(basename "$PWD")"
podman build -t "$NAME" .
```

Base images must be fully qualified (`docker.io/library/golang:1.26-bookworm`,
not `golang:1.26-bookworm`) — NixOS declares registries but never
`unqualified-search-registries`, so short names cannot resolve at all.

## Port 8080 is the Caddy-visible default — use it

Caddy on this machine routes:

```
https://printer-dashboard.local-ai-machine.johnsonlab.dev  ->  127.0.0.1:8080
```

So **bind `127.0.0.1:8080` and your instance is immediately reachable through
Caddy over HTTPS with no further setup** — that is the normal thing to do when
developing printer-dashboard, because it means the operator can look at your
work on their own device instead of you describing a screenshot to them.

```bash
NAME="printer-dashboard-$(basename "$PWD")"

podman rm -f "$NAME" || true      # never let a leftover from a prior run block you
podman run -d --name "$NAME" \
  --user 0:0 \
  -e DISABLE_CAMERAS=1 \
  -p 127.0.0.1:8080:8080 \
  -v "${HOME}/.printer-dashboard:/home/app/.printer-dashboard:rw" \
  -v "${HOME}/.printer-dashboard/config.yaml:/app/config.yaml:rw" \
  localhost/"$NAME":latest
```

Only one process can hold `:8080`, so if another worktree already has it,
check first (`ss -ltn | grep 8080`) and either take
over deliberately or publish a different loopback port — accepting that a
non-8080 instance is **not** reachable through Caddy and is therefore yours to
inspect via `localhost` only. Always bind loopback (`127.0.0.1:`); dev builds
ship with auth disabled and Caddy is the only intended public surface.

Publishing a *new* hostname is a deliberate human step (a Caddy route plus
recreating the proxy) — don't route around it.

Name the container after the checkout rather than using a fixed name, so
concurrent worktrees can't clobber each other's containers mid-test. The
`podman rm -f` line is not only about collisions: **a stopped-but-not-removed
container keeps holding printer resources**, and because the camera port is
single-session while the P1S local MQTT broker caps at roughly four clients, a
forgotten container degrades printers for every other instance — with errors
that blame the printer. Remove containers when you are done; do not leave them
stopped in the store.

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

- **a dev instance writing config rewrites the live printer list for every other
  instance on this machine**, because they all read the one file.
- **Signing in here can log production out.** This is cross-machine and the
  sharpest edge: production has its own `config.yaml` on its own host, so the
  file is not shared — but the Bambu *account* is. Tokens are single-use per
  login, so completing the sign-in wizard from a dev instance revokes the token
  the production machine is authenticating with. If you need to exercise the
  auth flow, copy the token store and point your mount at the copy. (Since the
  transport work, a revoked token degrades production to cloud-only rather than
  blinding it, and printers with local access keep streaming — but it still
  breaks remote access and should not happen casually.)
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
