# AGENTS.md — printer-dashboard

This project is docker-first for build/run verification. When building or running the
app in a container (locally, in CI, or to sanity-check a change), derive the image and
container names below from the current worktree so runs are reproducible and don't
collide with leftovers from a previous run — or with another agent's container.

This repo uses a fleet model where multiple AI worker agents may each be running
build/run verification concurrently, each in their own `.fleet/worktrees/<pet-name>/`
checkout. A single fixed image name, container name, host port, and volume path would
let two concurrently-running workers clobber each other's containers mid-test. Instead,
suffix the image/container name and the token-cache volume path with the current
worktree name, and publish the app port to a random host port instead of a fixed one:

- `WORKTREE` is derived automatically: it's set to the worktree's basename when running
  under `.fleet/worktrees/<pet-name>/`, and left empty for a normal single-worker
  checkout, in which case the commands below fall back to the plain `printer-dashboard`
  name exactly as before.
- Image and container name: `printer-dashboard`, suffixed with `-$WORKTREE` when set.
- Always remove any leftover container with that name — via `docker rm -f "$NAME" ||
  true` — immediately before `docker run`, so a stopped container from a prior run (or
  worktree) never blocks a new one.
- Host port: published as a random free port (`-p 0:8080`) rather than the fixed
  `8080:8080`, since a fixed host port would also collide across concurrent workers.
  Look it up after starting the container with `docker port "$NAME" 8080`.
- Token-cache volume: mounted from `${HOME}/.printer-dashboard-${WORKTREE:-default}`
  instead of a single shared `${HOME}/.printer-dashboard`, so concurrent workers don't
  share (and clobber) each other's cached Bambu tokens.

```bash
case "$(pwd)" in
  */.fleet/worktrees/*) WORKTREE=$(basename "$(pwd)") ;;
  *) WORKTREE="" ;;
esac

NAME="printer-dashboard${WORKTREE:+-$WORKTREE}"
docker build -t "$NAME" .

docker rm -f "$NAME" || true
docker run -d --name "$NAME" \
  -p 0:8080 \
  -v "${HOME}/.printer-dashboard-${WORKTREE:-default}:/home/app/.printer-dashboard:rw" \
  -v "$(pwd)/config.yaml:/app/config.yaml:rw" \
  "$NAME"

docker port "$NAME" 8080   # shows the assigned host port
```

The app has no required environment variables; it reads `config.yaml` (mounted at
`/app/config.yaml`) and listens on `:8080` inside the container. See the "Running with
Docker" section in README.md for details, including the optional token-cache volume
mount.

This is a single-container workflow by design — no docker-compose here (multi-service
orchestration is tracked separately, out of scope for this file).
