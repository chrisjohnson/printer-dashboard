# AGENTS.md — printer-dashboard

This project is docker-first for build/run verification. When building or running the
app in a container (locally, in CI, or to sanity-check a change), use the fixed image
and container names below so runs are reproducible and don't collide with leftovers
from a previous run.

- Image name: `printer-dashboard` (always this name, for both `docker build` and
  `docker run`).
- Container name: `printer-dashboard` (always this name).
- Always remove any leftover container with that name — via
  `docker rm -f printer-dashboard || true` — immediately before `docker run`, so a
  stopped container from a prior run never blocks a new one.

```bash
docker build -t printer-dashboard .

docker rm -f printer-dashboard || true
docker run -d --name printer-dashboard \
  -p 8080:8080 \
  -v "${HOME}/.printer-dashboard:/home/app/.printer-dashboard:rw" \
  -v "$(pwd)/config.yaml:/app/config.yaml:rw" \
  printer-dashboard
```

The app has no required environment variables; it reads `config.yaml` (mounted at
`/app/config.yaml`) and listens on `:8080`. See the "Running with Docker" section in
README.md for details, including the optional token-cache volume mount.

This is a single-container workflow by design — no docker-compose here (multi-service
orchestration is tracked separately, out of scope for this file).
