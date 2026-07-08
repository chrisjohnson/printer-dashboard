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

## Project Management

See [`KANBAN.md`](KANBAN.md) for the current task board and  
[`PLAN.md`](PLAN.md) for the full architecture plan.

## License

MIT
