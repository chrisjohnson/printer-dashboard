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

```bash
# Copy and edit the configuration (same as above)
cp config.example.yaml config.yaml
vim config.yaml

# Build the image
docker build -t printer-dashboard .

# Remove any leftover container from a previous run, then start a new one
docker rm -f printer-dashboard || true
docker run -d --name printer-dashboard \
  -p 8080:8080 \
  -v "${HOME}/.printer-dashboard:/home/app/.printer-dashboard:rw" \
  -v "$(pwd)/config.yaml:/app/config.yaml:rw" \
  printer-dashboard
```

Then open http://localhost:8080 in your browser.

Notes:
- The Bambu Lab token cache (`~/.printer-dashboard/`) lives inside the container's
  `$HOME` by default and will not persist across `docker rm`. Mount a volume at
  `/home/app/.printer-dashboard` if you want the Bambu token to survive container
  recreation (avoids re-authenticating after every restart).
- `-v "$(pwd)/config.yaml:/app/config.yaml:ro"` mounts the config read-only; drop `:ro`
  if you rely on the app's config `Save()` behavior and want it to persist back to disk.

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
