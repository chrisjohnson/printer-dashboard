# Printer Dashboard

A self-hosted web dashboard for monitoring and controlling multiple 3D printers from a single interface.

## Supported Printers

- **Bambu Lab P1S** — LAN MQTT protocol (pre-Bambu Connect firmware)
- **Bambu Lab H2S** — LAN MQTT protocol (Bambu Connect firmware)
- **Snapmaker U1** — Paxx custom firmware (REST + WebSocket)

## Features (planned)

- Real-time printer status (progress, temperatures, layers)
- Pause / Resume / Cancel / Skip Object
- Built-in and external camera feeds
- Job completion and error notifications
- Authentication for remote access
- Docker deployment

## Getting Started

```bash
# Copy and edit the configuration
cp config.example.yaml config.yaml
vim config.yaml

# Build and run
go build -o printer-dashboard .
./printer-dashboard
```

Then open http://localhost:8080 in your browser.

## Running with Docker

The app listens on `:8080` by default and reads its configuration from a `config.yaml`
file (no required environment variables — everything is configured via YAML). Mount your
`config.yaml` into the container at `/app/config.yaml`.

If you're working in one of this repo's fleet worktrees (`.fleet/worktrees/<pet-name>/`),
multiple agents may be building/running containers at the same time, so the image name,
container name, and host port are all derived from the current worktree instead of being
fixed — that way concurrent runs don't collide or clobber each other. `WORKTREE` is
detected automatically by the commands below; in a normal, single-worker checkout it
stays empty and the image/container name falls back to plain `printer-dashboard` exactly
as before (the host port is now always assigned randomly and looked up via
`docker port`, regardless of worktree). The token cache and `config.yaml` are mounted
from a single shared `~/.printer-dashboard/`, not per-worktree — see the notes below.

```bash
# Copy and edit the configuration into the shared, machine-wide location (same file
# used by every checkout — see notes below)
mkdir -p ~/.printer-dashboard
cp config.example.yaml ~/.printer-dashboard/config.yaml
vim ~/.printer-dashboard/config.yaml

# Automatically set when running under a fleet worktree (.fleet/worktrees/<pet-name>/);
# stays empty for a normal single-worker checkout
case "$(pwd)" in
  */.fleet/worktrees/*) WORKTREE=$(basename "$(pwd)") ;;
  *) WORKTREE="" ;;
esac

# Build the image
NAME="printer-dashboard${WORKTREE:+-$WORKTREE}"
docker build -t "$NAME" .

# Remove any leftover container from a previous run, then start a new one
docker rm -f "$NAME" || true
docker run -d --name "$NAME" \
  -p 0:8080 \
  -v "${HOME}/.printer-dashboard:/home/app/.printer-dashboard:rw" \
  -v "${HOME}/.printer-dashboard/config.yaml:/app/config.yaml:rw" \
  "$NAME"

# The host port is assigned randomly (to avoid colliding with other containers) —
# look it up here:
docker port "$NAME" 8080
```

Then open http://localhost:<port> in your browser, using the port from `docker port`
above.

Notes:
- Both the Bambu Lab token cache and `config.yaml` are mounted from a single shared
  `~/.printer-dashboard/`, not a per-worktree path. The token cache is small,
  written only when logging in to Bambu Cloud, and belongs to the cloud *account*
  rather than to any one worktree, so sharing it lets concurrent workers reuse an
  already-authenticated session instead of each having to log in fresh. Likewise,
  `config.yaml` describes the printers on this machine (their IPs, credentials), so
  it doesn't make sense to duplicate it per checkout — edit it once at
  `~/.printer-dashboard/config.yaml` and every worktree's container uses it.
- `-v "${HOME}/.printer-dashboard/config.yaml:/app/config.yaml:ro"` mounts the config
  read-only; drop `:ro` if you rely on the app's config `Save()` behavior and want it
  to persist back to disk.

## Testing

All new features are developed test-first (TDD). Tests use only the Go standard library
(plus `gorilla/websocket` for WebSocket handler tests). Mocks are hand-written in `_test.go` files.

```bash
# Run all tests
go test ./... -v -count=1

# With race detector (always run before committing)
go test ./... -race -count=1

# Coverage report
go test ./coverprofile=coverage.out
go tool cover -html=coverage.out
```

See [`PLAN.md`](PLAN.md) for the full architecture plan and testing standards.

## License

MIT
