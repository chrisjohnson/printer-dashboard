# Snapmaker U1 / Paxx firmware — exhaustive code findings

Scope: `internal/printers/snapmaker/snapmaker.go` (804 lines), `parser.go` (221),
`snapmaker_test.go` (1514), `parser_test.go` (530), `internal/printers/interface.go` (164),
plus supporting wiring in `internal/server/server.go`, `internal/server/onboarding.go`,
`internal/config/config.go`, `PLAN.md`, `.fleet/research-notes.md`, `.fleet/notes-archive.md`,
and the fleet cards K-005, K-077, K-079, K-081, K-091, K-092, K-095, K-096, K-103.

All `file:line` citations refer to the tree at this worktree's HEAD
(`cf5f938 feat: improve Snapmaker U1 homing detection and Z-jog UX` is the newest
commit touching the snapmaker package; `git log --oneline -- internal/printers/snapmaker/`).

---

## 0. Package-level framing

`snapmaker.go:1-4`:

```go
// Package snapmaker provides a printer client for Snapmaker U1 printers
// running Paxx firmware. Communication uses REST API for commands and
// WebSocket (or REST polling) for real-time status updates.
package snapmaker
```

The single most important historical fact about this driver is recorded in the
commit message of `4973f0a` ("Rewrite Snapmaker U1 client for actual Moonraker API
with multi-toolhead support"):

> The U1 runs Moonraker (Klipper) under the hood, **not the flat Paxx API we assumed**.

So the driver is a *hybrid*: three OctoPrint-compat / Paxx-flavoured `/api/...`
endpoints (kept from the first, wrong assumption) plus two genuine Moonraker-native
`/printer/...` endpoints added by the rewrite. `PLAN.md:190-200` still documents the
pre-rewrite endpoint table (including `/ws` and port 8080) and is **stale** — see §8.

Driver identity: `Type: "snapmaker"` (`snapmaker.go:106`), `HasChamber: false`
unconditionally (`snapmaker.go:107-108`, comment: `// U1 has no chamber heater — unconditionally false.`),
asserted by `TestNewPrinter_HasChamber_AlwaysFalse` (`snapmaker_test.go:60-76`).

---

## 1. HTTP / WebSocket endpoints

### 1.1 Base URL and port

`snapmaker.go:544-554`:

```go
// baseURL returns the HTTP base URL for the printer's REST API.
func (p *Printer) baseURL() string {
	if p.testBaseURL != "" {
		return p.testBaseURL
	}
	port := p.cfg.Port
	if port == 0 {
		port = 80
	}
	return fmt.Sprintf("http://%s:%d", p.cfg.Host, port)
}
```

- **Scheme is always plain `http://`** — no TLS/https path exists anywhere in the driver.
- **Default port is 80**, not 8080. Corrected by commit `a28d468`
  ("Fix Snapmaker default port from 8080 to 80"): *"U1 runs Moonraker under the hood
  which defaults to port 80, not 8080. User confirmed 8080 was wrong — their U1 needed port 80."*
  The onboarding form default is also 80 (`onboarding.go:3059`, hint text at `:3060`:
  *"The port the printer API listens on (default: 80)."*), and the onboarding handler
  defaults to 80 (`onboarding.go:338`). Port is validated 1–65535 at
  `onboarding.go:339-349` with error `Invalid port %q: must be a number between 1 and 65535`.
- `config.example.yaml:74-79` still shows `port: 8080` for the `u1` entry (stale example),
  while `tests/testdata/config.test.yaml` uses `host: 192.168.1.10`, `port: 80`,
  `access_code: ""` — corrected deliberately per `.fleet/notes-archive.md:264-266`
  ("correcting the `u1` Snapmaker entry's `host`/`port` to `192.168.1.10`/`80` to match
  `tests/camera.test.ts`'s hardcoded touchscreen-proxy expectation").

### 1.2 Authentication (`buildRequest`)

`snapmaker.go:556-571`:

```go
// buildRequest creates an HTTP request to the printer's REST API, injecting
// the access code as both a query parameter and a header.
func (p *Printer) buildRequest(method, path string, body io.Reader) (*http.Request, error) {
	url := p.baseURL() + path
	if p.cfg.AccessCode != "" {
		url += "?access_code=" + p.cfg.AccessCode
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if p.cfg.AccessCode != "" {
		req.Header.Set("X-Access-Code", p.cfg.AccessCode)
	}
	return req, nil
}
```

- Auth is **dual-delivered**: header `X-Access-Code` **and** query param `access_code`.
  Verified by `TestCommand_SendsAccessCode` (`snapmaker_test.go:317-349`).
- No token expiry, no session: `PLAN.md:202-204` — *"Access code sent as HTTP header
  `X-Access-Code` and query param `access_code`. No session or token expiry — same code
  reused for all requests."*
- Config field is shared with Bambu: `config.PrinterDef.AccessCode` (`config.go:47`,
  comment `// Bambu access code (for camera, optional without LAN mode)`). The Snapmaker
  onboarding form labels it neutrally: *"Access Code (optional) … If your Paxx firmware
  requires an API key or access code."* (`onboarding.go:3062-3064`).
- **Bug (latent): double `?` on the objects-query URL.** `fetchQueryStatus` passes a path
  that already contains a query string (`snapmaker.go:383`), and `buildRequest` appends
  `"?" + access_code` unconditionally. With an access code configured the request becomes:

  ```
  GET /printer/objects/query?print_stats&virtual_sdcard&gcode_move?access_code=CODE
  ```

  i.e. the third object name becomes `gcode_move?access_code` and the access code is not a
  real query param. No test covers `fetchQueryStatus` with a non-empty `AccessCode`
  (`snapmaker_test.go:1174-1207` uses no access code). Consequence would be a non-2xx from
  Moonraker → logged as `query poll failed (non-fatal)` (`snapmaker.go:214`) → progress,
  layers, filename and `PositionZ` silently stop updating.
- **Note on naming**: `access_code` is Bambu/OctoPrint vocabulary. Stock Moonraker's auth is
  `?access_token=` / `Authorization: Bearer <token>` (machine access token). Whether Paxx
  accepts `access_code`/`X-Access-Code` is unverified in this repo; the field is optional and
  the live fleet config uses `access_code: ""` for the U1, so the unauthenticated path is the
  one exercised in production.

### 1.3 Endpoint inventory (every call site)

| # | Method + path | Where | Request body | Expected response | Family |
|---|---|---|---|---|---|
| 1 | `GET /api/printer` | `snapmaker.go:349-377` (`fetchStatus`) | none | `apiPrinterResponse` JSON (§3.1) | OctoPrint-compat (Moonraker implements this subset) |
| 2 | `GET /printer/objects/query?print_stats&virtual_sdcard&gcode_move` | `snapmaker.go:382-410` (`fetchQueryStatus`) | none | `moonrakerQueryResponse` (§3.2) | **Moonraker-native** |
| 3 | `POST /api/print/pause` | `snapmaker.go:596-598` via `doCommand` (`:575-593`) | none (nil body) | any 2xx, body ignored | **Paxx-specific** (not stock Moonraker) |
| 4 | `POST /api/print/resume` | `snapmaker.go:601-603` | none | any 2xx | **Paxx-specific** |
| 5 | `POST /api/print/cancel` | `snapmaker.go:606-608` | none | any 2xx | **Paxx-specific** |
| 6 | `POST /api/printer/command` | `snapmaker.go:612-630` (`SkipObject`) | **none — nil body** | any 2xx, body ignored | OctoPrint-style; **sends no G-code at all** (§4.4) |
| 7 | `POST /printer/gcode/script` | `snapmaker.go:702-736` (`sendGCode`) | `{"script":"<raw gcode>"}`, `Content-Type: application/json` | `{"result":"ok"}` on success; may be HTTP 200 **with** `{"error":{"message":"…"}}` | **Moonraker-native** |
| 8 | `GET /webcam/stream.mjpg` | `snapmaker.go:792` (camera URL only) | — | MJPEG | Paxx/Fluidd host |
| 9 | `GET /screen/snapshot` | `snapmaker.go:793` (camera URL only) | — | PNG (touchscreen framebuffer) | Paxx/Fluidd host |
| 10 | `ws://host:port/websocket` | `snapmaker.go:338-345`, dialed at `:238` | — | see §2 | Moonraker-native WS path |

Notes:
- Endpoints 3–6 are exactly the pre-rewrite "reverse-engineered Paxx" list from
  `PLAN.md:192-200`, which also lists `/api/v2/camera/stream` (`GET`, MJPEG, *"unconfirmed,
  likely"*) — that camera endpoint was **never implemented**; the shipped camera paths are
  `/webcam/stream.mjpg` and `/screen/snapshot` instead.
- `PLAN.md:199` lists `/ws` as the WebSocket endpoint; the code uses `/websocket`
  (`snapmaker.go:342`), changed in `4973f0a` ("WebSocket endpoint changed from /ws to /websocket").
- Status-code check is a manual range test, not `resp.StatusOK`: `if resp.StatusCode < 200 || resp.StatusCode >= 300`
  (`snapmaker.go:362`, `:395`, `:588`, `:625`, `:723`).
- `fetchStatus`/`fetchQueryStatus` read the whole body with `io.ReadAll` and parse it;
  `doCommand`/`SkipObject` read the body **only on the error path** (`:589`, `:626`).
- HTTP clients: `httpClient` `Timeout: 10 * time.Second` (`snapmaker.go:110-112`);
  `gcodeClient` `Timeout: 120 * time.Second` (`snapmaker.go:113-115`). `gcodeClient` is used
  **only** by `sendGCode`. Rationale, quoted verbatim (`snapmaker.go:75-77`, repeated at `:113-114`):

  ```go
  // gcodeClient has a longer timeout for G-code commands that can
  // take >10s (e.g. G28 homing takes 30+ seconds on the U1).
  ```

  This is the K-096 timeout fix: K-096's original finding was *"`sendGCode()` in snapmaker.go:678-711
  makes a single synchronous HTTP POST with 10s timeout. No polling, no retries, no context-level
  timeout added at the handler layer"* (`.fleet/board/done/K-096-…md:29-32`). The 120 s client
  landed via `cf5f938` ("gcodeClient with 120s timeout for G28 homing (U1 takes 30+ seconds)").
  K-096 step 7 (*"add configurable timeout and retry for G-code/script endpoint"*) is still
  unchecked — **there is still no retry anywhere in this driver.**

### 1.4 Camera stream URLs and their auth

`snapmaker.go:762-795`:

```go
// CameraStreams returns the available camera/display streams for this printer.
// The U1 has a built-in webcam at /webcam/stream.mjpg on the printer's HTTP port
// and a touchscreen snapshot endpoint at /screen/snapshot.
// Users can add additional cameras via CameraDef in config.
```

- Returns `nil` when `cfg.Host == ""` (`:767-769`); default port 80 again (`:770-773`).
- `withAuth` (`:778-786`) uses proper `url.URL` query encoding here (unlike `buildRequest`),
  appending `?access_code=…`. Exact expected values asserted at `snapmaker_test.go:82-155`:

  ```
  http://192.168.1.100:8080/webcam/stream.mjpg?access_code=my-access-code   Type: "internal",    Label: "Camera"
  http://192.168.1.100:8080/screen/snapshot?access_code=my-access-code      Type: "touchscreen", Label: "Touchscreen"
  ```
- Frontend rewrites the touchscreen URL for the clickable link:
  `const interactiveUrl = escapeHtml(rawUrl.replace('/screen/snapshot', '/screen/'));`
  (`onboarding.go:1755`); the interactive page is at `/screen/` (comment at `snapmaker.go:789-791`:
  *"The U1 runs Fluidd (Klipper web UI) on port 80. The touchscreen snapshot endpoint at
  /screen/snapshot returns a PNG image. The interactive page is at /screen/ and is linked
  from the dashboard."*). Touchscreen frames are proxied through `/api/camera/frame?url=…`
  to avoid progressive rendering (`onboarding.go:1759-1768`).
- Playwright coverage: `tests/camera.test.ts:36-50` ("proxy serves snapmaker touchscreen
  snapshot"), `tests/dashboard.test.ts:50-54` (snapmaker printers must have camera streams).

---

## 2. WebSocket protocol

### 2.1 Dial

`snapmaker.go:232-246`:

```go
func (p *Printer) wsConnect(ctx context.Context, wsCh chan<- wsMsg) {
	wsURL := p.wsURL()
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		// Signal failure so the main loop starts fallback.
		select {
		case wsCh <- wsMsg{err: fmt.Errorf("ws dial: %w", err)}:
		case <-ctx.Done():
		}
		return
	}
```

- URL: `wsURL()` (`:337-345`) = `base` with `http` → `ws` prefix swap + `"/websocket"`.
  So `http://h:80` → `ws://h:80/websocket`. **No `access_code` / token is ever attached to
  the WS URL** and no `Authorization`/`X-Access-Code` header is passed (`DialContext(ctx, wsURL, nil)`
  — nil header map). An auth-required Moonraker WS would reject this.
- **No subprotocol** is requested (`Dialer{}` has no `Subprotocols` field set; grep for
  `Subprotocol`/`Subprotocols` in the package returns nothing).
- Library: `github.com/gorilla/websocket` (`snapmaker.go:19`).

### 2.2 Message envelope

The read loop (`snapmaker.go:301-334`):

```go
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			// Connection lost — signal fallback.
			select {
			case wsCh <- wsMsg{err: fmt.Errorf("ws read: %w", err)}:
			case <-ctx.Done():
			}
			return
		}

		status, err := parseAPIReport(message)
		if err != nil {
			// Malformed message — skip (keep connection alive).
			// WS may send JSON-RPC format; REST polling handles fallback.
			continue
		}
		…
```

- **Format assumed**: the *same flat shape as `GET /api/printer`* — i.e.
  `{"temperature":{…},"state":{"text":…,"flags":{…}}}`. Not JSON-RPC, not a
  `notify_status_update` envelope. Confirmed by `PLAN.md:209`
  (*"Status is pushed as same JSON format as `GET /api/printer`"*) and by the mock
  `mockPaxx.sendWS` which does `conn.WriteJSON(status)` with that exact shape
  (`snapmaker_test.go:1307-1323`, used at `:1408-1419`).
- **No objects are subscribed and no handshake message is ever sent.** There is no
  `WriteMessage`/`WriteJSON`/`subscribe`/`jsonrpc` call anywhere in `snapmaker.go`
  (verified by grep). The only frame the client ever writes is a control ping (`:283`).
  Stock Moonraker's `/websocket` is a JSON-RPC multiplexer that emits nothing until the
  client issues `printer.objects.subscribe`; `.fleet/research-notes.md:55` documents exactly
  that shape and it is **not used**:

  ```json
  {"jsonrpc":"2.0","method":"printer.gcode.script","params":{"script":"G28"},"id":1}
  ```

  The code hedges instead (`snapmaker.go:315-317`): *"Malformed message — skip (keep
  connection alive). WS may send JSON-RPC format; REST polling handles fallback."*
  Net effect: with stock Moonraker the WS never delivers a status message; every non-JSON
  or unexpected frame is silently `continue`d, and the read deadline (§2.3) eventually kills
  the connection. All real data therefore arrives via the always-on REST poll (§2.4).
- Malformed/unparseable messages are dropped **without logging** — no observability into
  "WS connected but sending nothing we understand".

### 2.3 Ping/pong, deadlines, timings

Constants (`snapmaker.go:25-32`):

```go
// Default intervals for the Connect lifecycle.
const (
	pollInterval    = 3 * time.Second  // REST status polling interval
	wsReconnectWait = 15 * time.Second // time between WebSocket reconnection attempts
	wsPingPeriod    = 30 * time.Second // WebSocket ping interval
	wsWriteWait     = 10 * time.Second // WebSocket write deadline
	wsReadWait      = 10 * time.Second // WebSocket read deadline
)
```

- Handshake timeout: `5 * time.Second` (`:235`).
- Read deadline set to `now + wsReadWait` (10 s) immediately after dial (`:260-267`),
  re-armed after every successful read (`:320-327`), and extended by the pong handler
  (`:269-272`):

  ```go
  // Configure pong handler to extend read deadline.
  conn.SetPongHandler(func(string) error {
  	return conn.SetReadDeadline(time.Now().Add(wsReadWait))
  })
  ```

- Ping sender (`:274-292`): `time.NewTicker(wsPingPeriod)` →
  `conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait))`; any write
  error makes the ping goroutine `return` silently (it does **not** tear the connection down
  or notify the main loop).
- **Timing inconsistency worth flagging**: `wsReadWait` (10 s) < `wsPingPeriod` (30 s). A
  pong can only arrive ~30 s after the last read, i.e. after the 10 s read deadline has
  already fired. The keepalive therefore cannot keep a quiet connection alive; the connection
  dies with `ws read: … i/o timeout` roughly 10 s after the last inbound frame. The test
  helper acknowledges the 10 s deadline as a shutdown hazard (`snapmaker_test.go:1348-1358`):

  ```go
  // Close the mock server FIRST to drop the WS connection before cancelling.
  // This lets wsConnect's ReadMessage error out immediately instead of
  // waiting for the read deadline (wsReadWait = 10s).
  ```

### 2.4 Connect lifecycle, reconnect and REST-poll fallback

`snapmaker.go:125-226`. Documented lifecycle (`:128-132`):

```go
// Lifecycle:
//  1. Fetch initial status from GET /api/printer
//  2. Dial WebSocket ws://host:port/ws for real-time updates
//  3. On WS failure, fall back to REST polling at pollInterval
//  4. Periodically attempt to re-establish the WebSocket
```

(Note the doc comment still says `/ws`; the code dials `/websocket`.)

1. **Initial snapshot** (`:134-145`): on failure, a *wholesale* status replacement with
   `State: "error"`, `ErrorMsg: fmt.Sprintf("initial status: %v", err)` (and `Online` left false).
2. `wsCh := make(chan wsMsg, 4)` (`:148`); WS runs in a goroutine under a derived cancelable
   ctx (`:151-159`).
3. **The poll ticker is created with `time.NewTicker` and never stopped except by `defer`**
   (`:161-164`), with this comment:

   ```go
   // REST polling timer — always active to fetch Moonraker query data
   // (gcode_move for homing state). WS-only path doesn't include this data.
   ```

   So REST polling is **always on at 3 s regardless of WS state**; `msg.err` merely
   `Reset()`s the already-running poll ticker and starts the WS-retry ticker (`:179-186`).
   A successful WS connect sends an empty `wsMsg{}` (`:294-299`) which the main loop
   deliberately ignores (no `err`, no `status`) — there is no "WS healthy, stop polling" path.
4. **Poll tick** (`:192-215`): `fetchStatus` then `fetchQueryStatus`. `fetchStatus` failure →
   wholesale status replacement:

   ```go
   p.setStatus(printers.PrinterStatus{
       ID: p.cfg.ID, Name: p.cfg.Name, Type: "snapmaker",
       Online: false, State: "error",
       ErrorMsg: fmt.Sprintf("status poll failed: %v", err),
   })
   ```

   `fetchQueryStatus` failure is non-fatal and only logged:
   `log.Printf("query poll failed (non-fatal): %v", qErr)` (`:214`).
5. **WS retry** (`:217-223`): on each `wsRetryTicker.C` tick a *new* `wsConnect` goroutine is
   launched and `wsWG.Add(1)` is called. The retry ticker is **never stopped after a
   successful reconnect**, so reconnect attempts keep being spawned every 15 s forever.
   Two latent races follow: (a) `wsWG.Add(1)` can run concurrently with `wsWG.Wait()` in the
   `ctx.Done()` branch (`:174-177`) — classic `sync: WaitGroup misuse` shape; (b) each
   `wsConnect`'s deferred cleanup sets `p.wsConn = nil` unconditionally (`:253-258`), so a
   stale attempt exiting can clear the pointer belonging to a newer live connection.
- `Connect` returns `ctx.Err()` (`:177`); `TestConnect_BlocksUntilCancelled`
  (`snapmaker_test.go:791-808`) asserts `context.Canceled`.
- Status fan-out: `setStatus` (`:530-542`) writes under `p.mu` then does a **non-blocking**
  send on `StatusCh` (`select { case p.StatusCh <- s: default: }`, comment
  `// Channel full, drop update (reader is slow)`). Tests: `TestSetStatus_StatusChNonBlocking`
  (`:180-196`), `TestSetStatus_StatusChDelivers` (`:198-211`), `TestSetStatus_StatusChNil`
  (`:213-220`). Server wires a **buffered 16** channel (`server.go:227-232`) and forwards via
  `startStatusForwarder` (`server.go:327-329`, `:342+`).
- `marshalStatus` (`snapmaker.go:512-518`) is **dead code** — grep shows no callers; its
  comment ("Used by the server in connectAllPrinters") is inaccurate.

---

## 3. Every Klipper/Moonraker object and field parsed → vendor-neutral mapping

### 3.1 `GET /api/printer` → `apiPrinterResponse` (`parser.go:10-105`)

```go
type temperatureEntry struct {
	Actual float64 `json:"actual"`
	Target float64 `json:"target"`
	Offset int     `json:"offset"`
}
```

`temperatureReport` has **dynamic keys** and a custom unmarshaler (`parser.go:18-46`):

```go
// temperatureReport captures all temperature entries from /api/printer.
// The JSON object has dynamic keys ("bed", "tool0", "tool1", ...) so we
// parse them into a map and provide helper methods for access.
type temperatureReport struct {
	Entries map[string]*temperatureEntry `json:"-"` // populated by UnmarshalJSON
}
```
- Explicit `null` handling → `nil` entry (`parser.go:34-38`, `// Explicitly handle JSON null — store nil entry.`).
- Malformed individual entries are **silently skipped** (`parser.go:40-42`, `// skip malformed entries`).
- `BedEntry()` reads key **`"bed"`** (`parser.go:49-54`).
- `ToolEntries()` reads keys prefixed **`"tool"`**, parses the index with
  `fmt.Sscanf(key, "tool%d", &idx)`, and sorts numerically (`parser.go:65-84`) —
  *"Handles any number of toolheads dynamically."*

```go
type stateFlags struct {
	Operational   bool `json:"operational"`
	Paused        bool `json:"paused"`
	Printing      bool `json:"printing"`
	Cancelling    bool `json:"cancelling"`
	Pausing       bool `json:"pausing"`
	Error         bool `json:"error"`
	Ready         bool `json:"ready"`
	ClosedOrError bool `json:"closedOrError"`
}

type stateReport struct {
	Text  string      `json:"text"`
	Flags *stateFlags `json:"flags,omitempty"`
}

type apiPrinterResponse struct {
	Temperature *temperatureReport `json:"temperature,omitempty"`
	State       *stateReport       `json:"state,omitempty"`
}
```

Real-hardware-shaped fixture (`parser_test.go:42-58`) — the U1 reports **four** toolheads:

```json
{
  "temperature": {
    "bed":   {"actual": 24.0, "offset": 0, "target": 0.0},
    "tool0": {"actual": 47.0, "offset": 0, "target": 0.0},
    "tool1": {"actual": 26.0, "offset": 0, "target": 0.0},
    "tool2": {"actual": 26.0, "offset": 0, "target": 0.0},
    "tool3": {"actual": 26.0, "offset": 0, "target": 0.0}
  },
  "state": {
    "text": "Operational",
    "flags": { "operational": true, "paused": false, "printing": false,
               "cancelling": false, "pausing": false, "error": false,
               "ready": true, "closedOrError": false }
  }
}
```

Null-temp fixture (`parser_test.go:118-136`) has `tool0/tool1/tool3: null` with only `tool2`
populated — i.e. the U1 does emit nulls for unheated/absent toolheads and the driver must
tolerate them.

**Mapping into `PrinterStatus`** (`snapmaker.go:449-471`):

| Source field | Destination |
|---|---|
| `temperature.bed.actual` / `.target` | `BedTemp` / `BedTargetTemp` (pointers) |
| `temperature.tool0.actual` / `.target` | `NozzleTemp` / `NozzleTargetTemp` **and** `NozzleTemps[0]` |
| `temperature.toolN.*` for all N | `NozzleTemps[]` entries `{Index, Actual, Target}` (sorted by index) |
| `state.text` + `state.flags` | `State` via `mapMoonrakerState` |
| (nothing) | `ChamberTemp`, `ChamberTargetTemp` — never set; `HasChamber=false` |
| (nothing) | `RemainingTime`, `ActiveTrayID`, `AMSUnits`, `HMSErrors` — never set by this driver |

`handleStatusReport` merges onto the existing status (`current := p.Status()`), so a report
with no `temperature` section **preserves** previous temps
(`TestHandleStatusReport_PartialUpdate`, `snapmaker_test.go:867-913`; WS variant
`TestConnect_WebSocketPartialUpdate`, `:1449-1514`). `Online` is forced `true` (`:416`).
`NozzleTemps` is rebuilt from scratch each report (`:455-470`), and `tool.Entry == nil`
entries are skipped (`:457-459`).

**State mapping** (`parser.go:194-221`):

```go
// mapMoonrakerState converts Moonraker state.text + flags into canonical state.
func mapMoonrakerState(stateText string, flags *stateFlags) string {
	// Priority order: flags first (more reliable), then text.
	if flags != nil {
		switch {
		case flags.Printing: return "printing"
		case flags.Paused:   return "paused"
		case flags.Error:    return "error"
		}
	}
	switch stateText {
	case "Operational":            return "idle"
	case "Printing":               return "printing"
	case "Paused":                 return "paused"
	case "Error":                  return "error"
	case "Complete", "Cancelled":  return "complete"
	default:                       return "unknown"
	}
}
```

- Flags win over text (test `"flags override text"`, `parser_test.go:314`).
- **The text match is case-sensitive and OctoPrint-capitalized.** Stock Moonraker's Klipper
  state strings are lowercase (`ready`/`standby`/`complete`/`cancelled`), so if Paxx passed
  those through, `idle`/`complete` would fall through to `"unknown"` (flags only rescue
  printing/paused/error). The live U1 evidently returns the capitalized forms — inferred
  because K-096's real-hardware jog flow passed the `State == "idle"` backend gate
  (`.fleet/board/done/K-096-…md:18-26`), which requires a literal `"idle"`.
- `"unknown"` has a hard consequence: `requireIdleAndOnline` (`server.go:959-967`) rejects
  anything that is not exactly `"idle"` with `printer is not idle (state: "unknown")` → HTTP 409
  on jog/home.
- `Cancelling`/`Pausing`/`Ready`/`ClosedOrError` are parsed but **never consulted**.

### 3.2 `GET /printer/objects/query?print_stats&virtual_sdcard&gcode_move` → `moonrakerQueryResponse` (`parser.go:107-142`)

```go
type printStatsInfo struct {
	CurrentLayer int `json:"current_layer"`
	TotalLayer   int `json:"total_layer"`
}

type printStatsReport struct {
	Filename      string          `json:"filename"`
	PrintDuration float64         `json:"print_duration"`
	State         string          `json:"state"`
	Message       string          `json:"message"`
	Info          *printStatsInfo `json:"info,omitempty"`
}

type virtualSDCardReport struct {
	Progress float64 `json:"progress"`
}

type queryStatus struct {
	PrintStats    *printStatsReport    `json:"print_stats,omitempty"`
	VirtualSDCard *virtualSDCardReport `json:"virtual_sdcard,omitempty"`
	GcodeMove     *gcodeMoveReport     `json:"gcode_move,omitempty"`
}

type gcodeMoveReport struct {
	HomedAxes  string    `json:"homed_axes"` // e.g. "xyz" when homed, "" when not
	Position   []float64 `json:"position"`   // [x, y, z, e] toolhead position
	HomingOrigin []float64 `json:"homing_origin"`
}

type moonrakerQueryResponse struct {
	Result struct {
		Status *queryStatus `json:"status"`
	} `json:"result"`
}
```

**Mapping** (`snapmaker.go:479-510`, `handleQueryReport`):

| Source | Destination | Notes |
|---|---|---|
| `print_stats.filename` | `CurrentFile` | **only if non-empty** (`if ps.Filename != ""`, `:486-488`) — see K-091 §5.4 |
| `print_stats.info.current_layer` | `CurrentLayer` | |
| `print_stats.info.total_layer` | `TotalLayers` | |
| `print_stats.print_duration` | — parsed, **unused** | |
| `print_stats.state` | — parsed, **unused** (state comes from `/api/printer` instead) | |
| `print_stats.message` | — parsed, **unused** | |
| `virtual_sdcard.progress` | `Progress` (0..1 float) | |
| `gcode_move.position[2]` | `PositionZ` | only if `len(Position) >= 3` |
| `gcode_move.homed_axes` | — parsed, **never used** (dead field) | |
| `gcode_move.homing_origin` | — parsed, **never used** | |

The `gcode_move` block carries the single most load-bearing Paxx comment in the file
(`snapmaker.go:497-507`):

```go
	if gm := q.Result.Status.GcodeMove; gm != nil {
		// Paxx firmware doesn't expose homed_axes in gcode_move status.
		// We track homing state via G28 commands sent from the frontend
		// (handleHomeAll in server.go marks homed=true on G28 success).
		// Clear homed when a print starts (updateCard in onboarding.go).
		// Don't try to infer from Z position — Paxx parks Z at ~9.9mm
		// after homing, which is far from the bed.
		if len(gm.Position) >= 3 {
			current.PositionZ = gm.Position[2]
		}
	}
```

`handleQueryReport` is a **partial merge** — it only touches the fields above and preserves
everything else (`TestHandleQueryReport_PreservesExistingState`, `snapmaker_test.go:1076-1112`),
and is nil-safe (`TestHandleQueryReport_NilReport`, `:1114-1124`; guard at `:480-482`).

Fixture with the full Moonraker shape (`parser_test.go:190-206`) — note `eventtime`,
`total_duration` and `filament_used` are present on the wire but **not modeled**:

```json
{"result":{"eventtime":12345678,"status":{
  "print_stats":{"filename":"test.gcode","total_duration":28185.7,"print_duration":28125.6,
                 "filament_used":88827.7,"state":"printing",
                 "info":{"total_layer":65,"current_layer":42}},
  "virtual_sdcard":{"progress":0.5}}}
```

### 3.3 Objects NOT queried (documented gaps)

- `led` — deliberately excluded. `K-103` research: *"`snapmaker.go:382-383` — `fetchQueryStatus()`
  calls `GET /printer/objects/query?print_stats&virtual_sdcard&gcode_move`. Only three objects
  queried — `led` is NOT included."* Live-confirmed useless anyway (§5.2).
- `toolhead` — `.fleet/research-notes.md:52` notes *"`GET /printer/objects/query?toolhead`
  returns live position `[x,y,z,e]`"*, but the code reads position from `gcode_move` instead.
- `filament_feed`, `filament_detect` — **not queried, not parsed, zero implementation.**
  Fully specified in `.fleet/research-notes.md:177-226` (see §6.3) but nothing in
  `internal/printers/snapmaker/` references them (grep for `filament|RFID|FEED_AUTO` in the
  package returns only test fixtures and comments). `PrinterStatus.AMSUnits`/`ActiveTrayID`
  (`interface.go:56-63`) are Bambu-only; `ActiveTrayID` is left at its zero value `0` for
  Snapmaker (documented as `-1 when unknown or not applicable`, `interface.go:60-62`) — a
  small vendor-leak bug: `0` means "AMS 0 tray 0" to the frontend, not "unknown".
- `output_pin`, `webhooks`, `printer`, `server_info`, `print_stats.total_duration`,
  `filament_used` — never requested/parsed.

### 3.4 Moonraker embedded-error envelope (`parser.go:144-172`)

```go
// moonrakerErrorResponse captures the embedded-error shape Moonraker can
// return alongside an HTTP 200, e.g. {"error": {"message": "Klippy Not
// Connected"}}. A successful response (e.g. {"result": "ok"}) leaves Error
// nil.
type moonrakerErrorResponse struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}
```

`parseMoonrakerError` (`:160-172`) returns `nil` for empty body, unparsable body, or no
`error` field — *"callers should treat those as success"* — otherwise
`fmt.Errorf("moonraker error: %s", r.Error.Message)`.

---

## 4. Every G-code / Klipper command emitted verbatim

### 4.1 Inventory

| Command string (verbatim) | Emitted by | Endpoint |
|---|---|---|
| `SET_LED LED=cavity_led RED=1 GREEN=1 BLUE=1 WHITE=1` | `SetLight(ctx, true)` `snapmaker.go:653` | `POST /printer/gcode/script` |
| `SET_LED LED=cavity_led RED=0 GREEN=0 BLUE=0 WHITE=0` | `SetLight(ctx, false)` `snapmaker.go:655` | same |
| `G28` | `HomeAll` `snapmaker.go:672` | same |
| `G91\nG1 X%g Y%g Z%g F%d\nG90` (only non-zero axes) | `Jog` `snapmaker.go:683-697` | same |

Exact request body shape (`snapmaker.go:703`, `Content-Type: application/json` at `:713`):

```json
{"script":"G91\nG1 X10 Y-5 F1500\nG90"}
```

Asserted verbatim in tests (`snapmaker_test.go:659-675`):

```
"G91\nG1 X10 Y-5 F1500\nG90"     // x=10, y=-5, z=0, speed=1500
"G91\nG1 Z2.5 F600\nG90"         // z-only
"G91\nG1 X1 Y2 Z3 F3000\nG90"    // all three
```

`Jog`'s doc comment (`snapmaker.go:675-682`):

```go
// Jog moves the toolhead by the given relative deltas (mm) at the given
// feedrate (mm/min). It switches to relative positioning (G91), issues a
// single G1 move containing only the axes with a non-zero delta, and
// restores absolute positioning (G90) afterward so subsequent commands
// aren't accidentally interpreted as relative moves — same shape as Bambu's
// jogCommand, adapted to a plain multi-line G-code script (sendGCode takes
// the raw script; no JSON command wrapping needed here, Moonraker's
// gcode/script endpoint does that).
```

`SetLight`'s doc comment records a real-hardware finding (`snapmaker.go:647-651`):

```go
// SetLight turns the cavity LED on or off via Moonraker's GCode endpoint.
//
// The cavity_led fixture is RGB-driven (it appears white via R=G=B=1), not
// white-channel-driven, so all four channels must be set together — setting
// only WHITE is a no-op on the real hardware.
```

### 4.2 Speed / feedrate constants

- Driver has **no** speed constants — `speedMMPerMin` is passed in. Defaults and clamps live
  in the server (`server.go:931-945`):

  ```go
  const (
  	// jogMaxDeltaMM is the maximum absolute distance (mm) allowed on any
  	// single axis in one jog request. 100mm is a conservative cap chosen to
  	// be safely within the smallest build volume across supported printer
  	// models, regardless of which printer the request targets — it's a
  	// blunt, model-agnostic safety limit, not a precise per-printer bound.
  	jogMaxDeltaMM = 100.0

  	// jogDefaultSpeedMMPerMin is used when the request omits "speed".
  	jogDefaultSpeedMMPerMin = 1500

  	// jogMaxSpeedMMPerMin is the maximum feedrate (mm/min) accepted for a jog
  	// request.
  	jogMaxSpeedMMPerMin = 6000
  )
  ```
- Frontend step selector offers `0.1 / 1 / 10 / 100 mm`, default **0.1 mm**
  (`onboarding.go:1873-1878`; fallback `getStepSize` returns `0.1`, `:2055-2063`).
- `.fleet/research-notes.md:35` also documents fan control `{"script":"M106 S{0-255}"}` for
  the U1 — **never implemented** (no `M106` anywhere in the repo's Go code).

### 4.3 Safety gating

- **Must-be-idle**: `requireIdleAndOnline` (`server.go:947-967`) is the only real backend gate
  in the app and applies to `POST /api/printers/{id}/home` and `POST /api/printers/{id}/jog`
  only:

  ```go
  func requireIdleAndOnline(status printers.PrinterStatus) error {
  	if !status.Online {
  		return fmt.Errorf("printer is offline")
  	}
  	if status.State != "idle" {
  		return fmt.Errorf("printer is not idle (state: %q)", status.State)
  	}
  	return nil
  }
  ```
  Callers respond **409 Conflict**. Section comment (`server.go:920-929`) states these are
  *"the first command handlers in this app with real backend-side safety gating … K-092
  (backlog) tracks retrofitting the same gating onto the existing (currently ungated) command
  handlers."* K-092 confirms pause/resume/cancel/temp/light are **frontend-disable-only**
  and *"trivially bypassable via a direct API call"*
  (`.fleet/board/backlog/K-092-…md:16-25`).
- **Must-home**: not a backend gate — it is discovered *from the firmware's rejection* (§5.1).
- **Jog input validation** (`server.go:1056-1084`): NaN/Inf → `"%s must be a finite number"`;
  `|delta| > 100` → `"%s exceeds max jog distance of %gmm"`; `speed <= 0 || speed > 6000`
  → `"speed must be > 0 and <= %d mm/min"`. Deliberate no-silent-clamping rationale at
  `server.go:1014-1018`. `x=y=z=0` is accepted as a harmless no-op (`:1028-1032`).
- **Frontend gates**: jog buttons disabled unless `p.online && state === 'idle'`
  (`onboarding.go:1865-1868`), with the honest comment (`:1862-1864`):
  *"This is a UX nicety only: the backend's requireIdleAndOnline is the real enforcement
  (see server.go), so the exact disabled condition here doesn't need to be perfectly
  authoritative."* Z-jog always goes through a confirmation modal (collision risk) unless
  `homed === true` (`onboarding.go:2070-2087`, `:2144-2179`). `_jogBusy[id]` prevents
  queuing moves during an in-flight jog/home (`:2071`, `:2029`, `:2099`).
- **No firmware ack anywhere**: `sendGCode` returns as soon as Moonraker accepts the script.
  `onboarding.go:2089-2093`: *"The backend cannot confirm the toolhead physically finished
  moving (no firmware ack) — a 200 here only means the command was accepted and sent, not
  that the move completed."* Same finding recorded in K-081's working context:
  *"UI copy should say 'command sent,' not 'printer moved.'"*

### 4.4 Skip-object: the command that sends nothing

`snapmaker.go:610-630`:

```go
// SkipObject sends a G-code command to skip the current object.
// Uses POST /api/printer/command with the skip G-code.
func (p *Printer) SkipObject(ctx context.Context) error {
	req, err := p.buildRequest(http.MethodPost, "/api/printer/command", nil)
	…
```

**The body is `nil` — no G-code is sent.** The doc comment is inaccurate. K-076's working
context records this as known: *"**Snapmaker**: POST to `{host}:{port}/api/printer/command`
with no body (`snapmaker/snapmaker.go:549-569`)"* and *"**No Klipper/Moonraker EXCLUDE_OBJECT**
integration exists"* (`.fleet/board/done/K-076-…md`). The test
(`snapmaker_test.go:303-315`) uses `mockPaxxServer` which only asserts path + method, never
the body — so the empty body is untested-by-design. K-076's Inspector pass reported *"Both
Bambu (MQTT) and Snapmaker (HTTP POST) firmware commands fire without error"*, i.e. the
endpoint returned 2xx on the live U1 without producing an object-level exclusion.
`handleSkipObject` (`server.go:787-809`) records the skip in a purely local in-memory
`SkipTracker` regardless, so the UI badge advances even though the printer did nothing.

### 4.5 Commands the interface demands but the driver refuses

`snapmaker.go:632-645` — all three return hard errors despite being trivially implementable
as `M140 S…` / `M104 S…` (both are specified for the U1 in `.fleet/research-notes.md:28-29`):

```go
// SetBedTemp is not supported on Snapmaker U1.
func (p *Printer) SetBedTemp(_ context.Context, _ int) error {
	return fmt.Errorf("snapmaker %s: set bed temp not implemented", p.cfg.ID)
}

// SetNozzleTemp is not supported on Snapmaker U1.
func (p *Printer) SetNozzleTemp(_ context.Context, _ int) error {
	return fmt.Errorf("snapmaker %s: set nozzle temp not implemented", p.cfg.ID)
}

// SetChamberTemp is not supported on Snapmaker U1 (no chamber heater).
func (p *Printer) SetChamberTemp(_ context.Context, _ int) error {
	return fmt.Errorf("snapmaker %s: set chamber temp not implemented", p.cfg.ID)
}
```

---

## 5. Real-hardware quirks and gotchas (verbatim comments + ticket evidence)

### 5.1 The "Must home" UX problem (K-096, K-095)

Live error, quoted verbatim from `.fleet/board/done/K-096-snapmaker-u1-gcode-request-timeout.md:18-26`:

```
Must home Z axis first: 5.130 4.947 199.982 [11251.005]
```

> This is a Klipper behavior: after power cycle or when Klippy reconnects, Klipper loads the
> last known Z position from its saved config/position, so the coordinates show Z at ~200mm.
> **But Klipper still requires a fresh physical Z home** before allowing any movement or homing
> commands. The dashboard's client-side home state tracker thinks the printer is homed
> (because K-081 tracks homing via G28 commands), but Klipper itself has not been physically
> homed for Z.
>
> This creates a false sense of readiness: the dashboard shows the printer as "homed" and
> allows jog commands, but Klipper silently has no valid Z home, and when the user tries to
> home or move Z, they get the "Must home Z axis first" error.

Driver-side detection (`snapmaker.go:738-760`):

```go
// maybeMarkUnhomed checks an error response body for "Must home" messages
// and, if found, sets status.Homed to false so the frontend can offer
// homing (see openZJogModal which checks cache.homed === false).
func (p *Printer) maybeMarkUnhomed(respBody []byte) {
	bodyStr := string(respBody)
	if !strings.Contains(bodyStr, "Must home") {
		return
	}
	p.mu.Lock()
	if p.status.Homed != nil && !*p.status.Homed {
		p.mu.Unlock()
		return
	}
	p.status.Homed = boolPtr(false)
	s := p.status
	p.mu.Unlock()
	if p.StatusCh != nil {
		select {
		case p.StatusCh <- s:
		default:
		}
	}
}
```

Called from both failure branches of `sendGCode` (`snapmaker.go:725`, `:732`) — i.e. on a
non-2xx **and** on an HTTP-200-with-embedded-error.

Server-side counterpart (`server.go:984-998`), whose comment is the clearest statement of
the bug:

```go
	if err := p.HomeAll(r.Context()); err != nil {
		// Paxx/Snapmaker firmware may accept the G28 HTTP request (200)
		// but return an embedded Klipper error (e.g. "Must home Z axis
		// first").  sendGCode detects this via maybeMarkUnhomed and
		// mutates status.Homed = false, but handleHomeAll still sees
		// a nil error and would mark homed = true on the next line —
		// the exact bug reported for the U1.  Detect "Must home" here
		// so we don't clobber the backend state.
		if strings.Contains(err.Error(), "Must home") {
			writeError(w, 400, err.Error())
			return
		}
		writeError(w, 500, err.Error())
		return
	}
```

Frontend counterpart (`onboarding.go:2095-2123`):

```js
    // Special case: if the error contains "Must home" (e.g. Paxx/Snapmaker
    // firmware requires a physical Z home after power cycle), update the
    // cache to homed=false and offer homing instead of a raw alert.
…
            if (d.error && d.error.indexOf('Must home') !== -1) {
              // Klipper rejected the jog — printer needs a physical home.
              // Update cache so the next Z-jog offers homing first.
              if (!window._printerCache[id]) window._printerCache[id] = {};
              window._printerCache[id].homed = false;
              updateCard(window._printerCache[id]);
              openZJogModal(id, body.z || body.x || body.y, body);
            } else {
              alert(d.error || 'Command failed');
            }
```

K-095 turned the warning into an auto-home: modal title `"Printer not homed"`, text
`"Move Z by <delta>mm, but the printer is not homed. Run homing first?"`, confirm button
`"Run Homing"` → `homeAll(id, function() { sendJog(id, body); })`
(`onboarding.go:2155-2165`; card `.fleet/board/done/K-095-…md:40-48`).
Test: `server_test.go:2134-2154` "400 on Must home error — does not mark homed", using the
fixture error string

```
snapmaker printer-1: gcode returned HTTP 400: {"error": {"code": 400, "message": "Must home Z axis first: 5.130 4.947 199.982"}}
```

Note the fixture shows Moonraker's real error body also carries a numeric `"code"` field,
which `moonrakerErrorResponse` does **not** model (only `message`).

### 5.2 Cavity LED: never reports back, stuck on "-" (K-077, K-103)

Struct field comment (`snapmaker.go:57-62`) — the canonical statement:

```go
	// lightOn tracks the last-commanded cavity LED state. Moonraker/Klipper
	// provides no way to query real LED state for this fixture (the
	// led object query always returns null), so we track the last state we
	// successfully commanded instead of polling hardware. nil means unknown
	// (no SetLight call has succeeded yet). Guarded by mu, same as status.
	lightOn *bool
```

Read back into status every cycle (`snapmaker.go:418-423`):

```go
	// Light state — Moonraker/Klipper cannot report real LED state for this
	// fixture (the led object query always returns null), so populate from
	// the last state we successfully commanded via SetLight instead.
	p.mu.RLock()
	current.LightOn = p.lightOn
	p.mu.RUnlock()
```

K-077 root cause, verbatim from `.fleet/notes-archive.md:28-33`:

> (1) shipped code sent `SET_LED LED=cavity_led WHITE=1/0` but this fixture is RGB-driven,
> not white-channel-driven — the WHITE-only command was a hardware no-op; (2) the Snapmaker
> driver never populated `PrinterStatus.LightOn`, and Moonraker's LED-state query always
> returns null on this hardware (confirmed live) so state can't be polled — fixed by tracking
> last-commanded state in-memory instead.

And from the plan entry (`.fleet/notes-archive.md:402-406`): *"`handleStatusReport()` … should
populate `PrinterStatus.LightOn` from this tracked field — NOT from any Moonraker query, since
`GET /printer/objects/query?led=cavity_led` is confirmed to always return null on this hardware"*.

K-103 closed it as **won't-fix** (`.fleet/board/done/K-103-…md:51-52`):

> **No real-state query path exists.** Confirmed via code review: Paxx firmware does not expose
> `led` state through Moonraker's `QUERY_LED`, `output_pin`, or any other Klipper-level
> alias/custom command. The U1's REST API (`/api/printer`) and WS endpoint do not expose LED
> state either. The `led` object query returns null (confirmed live in K-077). …
> **Ticket closed as accepted.** The current behavior is acceptable: "-" shows when state is
> unknown (initial load, power cycle), and the user can toggle via the existing toggle control.

Also from K-103: *"If the LED is changed via any other path (touchscreen, another Moonraker
client, power cycle, Klipper macro), the dashboard state diverges from reality and stays
stale."* — the same limitation flagged earlier as review finding (b)
(`.fleet/notes-archive.md:448-454`). Contrast with Bambu, which reads real
`print.lights_report` MQTT data (`.fleet/notes-archive.md:422-427`).

Commanded-state tracking is **success-only**, enforced by three tests:
`TestSetLight_TracksCommandedState` (`snapmaker_test.go:553-587`),
`TestSetLight_HTTPError_DoesNotTrackState` (`:589-606`),
`TestSetLight_200WithEmbeddedError_DoesNotTrackState` (`:758-775`),
`TestHandleStatusReport_LightOnUnknownByDefault` (`:777-785`).

### 5.3 Z-home desync and the ~9.9 mm park

- `snapmaker.go:502-503`: *"Don't try to infer from Z position — Paxx parks Z at ~9.9mm after
  homing, which is far from the bed."*
- `server.go:999-1003`:

  ```go
  // Mark as homed — Paxx/Snapmaker don't expose homed_axes, so we
  // infer it from the successful G28 command. The printer will park
  // Z at ~9.9mm but is technically homed.
  homed := true
  p.SetHomed(&homed)
  ```
- `onboarding.go:2040-2045` (same reasoning client-side):
  *"Paxx firmware doesn't expose homed_axes, so mark as homed immediately on successful G28 —
  the printer will park at ~9.9mm but is technically homed."*
- `onboarding.go:1170-1175` — homed is cleared when a print starts:

  ```js
      // When a print starts, clear homed state — the printer will move
      // down to the bed and the cached "homed" flag from G28 is no longer
      // valid (Paxx doesn't expose homed_axes to track this).
      if (isActivePrint && !wasActivePrint && window._printerCache[p.id]) {
        window._printerCache[p.id].homed = null;
      }
  ```
- `onboarding.go:1088-1092` — the client also refuses to let a server `homed:false` overwrite a
  locally-cached `homed:true`:

  ```js
      // Also skip homed: false from server when we just homed (cached true)
      // — Paxx firmware doesn't expose homed_axes, so we track it via G28.
  …
        if (k === 'homed' && cached[k] === true && p[k] === false) return;
  ```
- Interface-level admission (`interface.go:51-55`):

  ```go
  	// Homed reports whether the printer's axes are homed, when known. nil
  	// means unknown (not yet reported, or not supported by this driver —
  	// e.g. Snapmaker has no homed-state query wiring today), true = homed,
  	// false = not homed.
  	Homed *bool `json:"homed,omitempty"`
  ```
  and `SetHomed` (`interface.go:160-163`, `snapmaker.go:797-804`):
  *"Used when firmware doesn't expose homed_axes (e.g. Paxx/Snapmaker U1). nil clears the
  cached homed state."*
- K-096 steps 4–6 (proactive Z-home truth detection via `printer.query_endstops` etc.) remain
  **unchecked** — deferred: *"Steps 4-7 (proactive Klipper Z home state research, original 10s
  timeout fix) are deferred to future tickets if the user reports issues."*

### 5.4 `currentFile` never clears (K-091, open)

`.fleet/board/backlog/K-091-snapmaker-currentfile-never-clears.md:19-30`, verbatim:

> Bambu's `CurrentFile` clearing (added by K-003, commit `ee5445d`) lives in
> `internal/printers/bambu/client.go:439` and is keyed off the derived `State` field reaching
> `"idle"`. Snapmaker has no equivalent at all: `CurrentFile` is populated in `handleQueryReport`
> … from Moonraker's `print_stats.filename` — a completely separate code path from
> `handleStatusReport`'s `State` latch … — and nothing ever clears it. Moonraker's
> `print_stats.filename` typically stays populated until a *new* print starts in stock Klipper
> (doesn't go empty on completion), so in practice the dashboard likely shows a Snapmaker
> printer's last-printed filename indefinitely after it finishes, until the next print begins.

Confirmed in code: the only write is `snapmaker.go:486-488`, guarded by `if ps.Filename != ""`,
so an empty server value cannot clear it either. Card is **not started**.

### 5.5 The `complete` → `idle` flicker latch (K-004/K-006/K-030)

`snapmaker.go:34-44`:

```go
// completeIdleStreakThreshold is the number of consecutive non-"complete"
// reports required before handleStatusReport allows a newly-reported state
// to overwrite a latched State="complete" back down to a non-terminal state
// such as "idle". Mirrors the same latch in the Bambu client (see
// completeIdleStreakThreshold there for the full rationale): Moonraker's
// "Complete"/"Cancelled" status tends to be stickier than Bambu's SUCCESS,
// but the same architectural gap exists, so we apply the same guard for
// consistency. Any report that maps to "printing" (a new print starting)
// still overrides "complete" immediately — only settling back to a
// non-printing state is latched.
const completeIdleStreakThreshold = 2
```

Implementation (`snapmaker.go:425-447`) and tests (`snapmaker_test.go:959-1038`: single
`Operational` keeps `complete`; two settle to `idle`; `Printing` overrides immediately).
Commit `e746715` notes the Snapmaker half was applied *"for architectural consistency, even
though Moonraker's Complete status tends to be stickier in practice than Bambu's SUCCESS."*
Minor correctness nit: the field comment says *"Guarded by mu, same as status"*
(`snapmaker.go:69-70`) but `handleStatusReport` mutates `p.completeIdleStreak` **without**
taking `p.mu` (`:438-445`) — safe only because it is driven by the single `Connect` goroutine.

### 5.6 The 200-with-embedded-error blind spot (K-077 fix, K-079 still open)

`snapmaker.go:729-734`:

```go
	// Moonraker can return HTTP 200 with an embedded error in the JSON body
	// (e.g. klippy not connected, invalid GCode) — check for that too.
	if err := parseMoonrakerError(respBody); err != nil {
		p.maybeMarkUnhomed(respBody)
		return fmt.Errorf("snapmaker %s: gcode failed: %w", p.cfg.ID, err)
	}
```

`.fleet/notes-archive.md:442-447` explains why this mattered: *"`sendGCode` … treats any HTTP
2xx as success without inspecting Moonraker's JSON body for an embedded error field (e.g.
"Klippy Not Connected") — pre-existing behavior, not introduced by this diff, but K-077's new
`lightOn` tracking now leans on that success signal more heavily (previously a wrong signal had
no visible effect since nothing read it back)."*

**K-079 is still open** (`.fleet/board/backlog/K-079-…md:17`): *"Other Snapmaker Moonraker call
sites may share the 200-with-embedded-error blind spot. Audit doCommand, SkipObject,
fetchStatus, fetchQueryStatus for the same pattern fixed in K-077."* Verified: `parseMoonrakerError`
is called **only** from `sendGCode`. `doCommand` (`:588-592`), `SkipObject` (`:625-629`),
`fetchStatus` (`:362-376`) and `fetchQueryStatus` (`:395-409`) all treat any 2xx as success.
A `{"error":{"message":"Klippy Not Connected"}}` on `/api/print/pause` would be reported to the
user as a successful pause.

### 5.7 Snapmaker `ErrorMsg` not rendered (fixed)

`.fleet/notes-archive.md:207`: *"**Fixed bug**: Snapmaker U1 `ErrorMsg` not shown — now renders
as red error banner."*

### 5.8 Multi-toolhead count

Commit `4973f0a`: *"Added `NozzleTempEntry` struct + `NozzleTemps []NozzleTempEntry` to
`PrinterStatus`. All 4 toolhead temps (T0-T3) now parsed and exposed in API/UI. Tool0 mapped
to existing `NozzleTemp`/`NozzleTargetTemp` for backward compat. `ChamberTemp` removed from
parser (U1 has no chamber sensor)."* `.fleet/notes-archive.md:920`: *"Snapmaker supports N
nozzles dynamically; Bambu currently single-nozzle."*

### 5.9 Test smells worth knowing

- `TestCommand_Unreachable` (`snapmaker_test.go:385-403`) and `TestSetLight_ContextCancelled`
  (`:535-551`) use `context.WithTimeout(context.Background(), 10)` — a bare `10`, i.e.
  **10 nanoseconds**, under the comment `// Shorten context timeout so the test doesn't hang.`
  They pass because the context is already expired, not because of any timeout logic.
- `mockPaxxServer` (`:230-259`) asserts only path and method — never the body — which is why
  `SkipObject`'s empty body and `doCommand`'s lack of error-body inspection are invisible.
- `mockGCodeServer` (`:412-449`) asserts path, method and `Content-Type: application/json`,
  and replies `{"result": "ok"}` on 2xx.
- `mockSnapmakerServer` (`:1228-1294`) implements `/api/printer`, `/printer/objects/query`
  and `/websocket`, and its WS handler only blocks on reads — it never pushes unsolicited
  frames, so the "WS sends nothing" production behaviour is never exercised.
- `TestConnect_FetchInitialStatus` waits up to 1 s for the WS upgrade
  (`:1370-1375`) and asserts `State == "idle"` from the mock's `"Operational"` text.

---

## 6. What Paxx changes vs stock Snapmaker firmware (and where the code bends)

### 6.1 Naming and framing

- Package doc calls it *"Paxx firmware"* (`snapmaker.go:2`); the onboarding page says
  *"Enter the network details for your Snapmaker U1 running Paxx firmware."*
  (`onboarding.go:3046`); the handler section header is `// Handlers — Snapmaker (local Paxx firmware)`
  (`onboarding.go:318`).
- The fleet brief describes Paxx as *"community firmware; Klipper + Moonraker based"*. The code
  agrees, retroactively: `4973f0a`'s message says the U1 *"runs Moonraker (Klipper) under the
  hood, not the flat Paxx API we assumed."*
- `.fleet/research-notes.md:51`: *"Runs Klipper + Moonraker — all control goes through
  `POST /printer/gcode/script`."*
- `snapmaker.go:789`: *"The U1 runs Fluidd (Klipper web UI) on port 80."*

### 6.2 Concrete Paxx-specific accommodations in the code

| Accommodation | Evidence |
|---|---|
| `homed_axes` is **not exposed** → homing inferred from G28 success, cleared on print start, forced false on "Must home" | `snapmaker.go:497-503`, `:797-804`; `server.go:999-1003`; `onboarding.go:1088-1092`, `:1170-1175`; `interface.go:51-55`, `:160-163` |
| `led` object query **always returns null** → commanded-state tracking instead of polling | `snapmaker.go:57-62`, `:418-423`; K-077/K-103 |
| `cavity_led` is **RGB-driven, not white-driven** → all four channels set together | `snapmaker.go:649-655`; K-077 |
| **G28 takes 30+ s** → separate 120 s HTTP client | `snapmaker.go:75-77`, `:113-115`; commit `cf5f938` |
| **Parks Z at ~9.9 mm** after homing → Z position must not be used to infer homed | `snapmaker.go:502-503`; `server.go:1000-1001` |
| Klipper keeps a **saved Z position** across restarts but demands a fresh physical home → "Must home Z axis first" | K-096; `snapmaker.go:738-760`; `server.go:984-995` |
| Moonraker returns **HTTP 200 with embedded error** → `parseMoonrakerError` | `snapmaker.go:729-734`; `parser.go:144-172` |
| Moonraker's `/api/printer` exposes **N toolheads under dynamic keys** (`tool0..tool3`) | `parser.go:18-46`, `:65-84` |
| **No chamber heater** → `HasChamber=false` unconditionally, `SetChamberTemp` refused | `snapmaker.go:107-108`, `:642-645`; `dashboard.test.ts:87-89` |
| Moonraker's `print_stats.filename` **doesn't clear on completion** → stale filename (open) | K-091 |
| Touchscreen framebuffer exposed at `/screen/snapshot`, interactive at `/screen/` | `snapmaker.go:789-793`; `onboarding.go:1755` |
| Bambu-first design mandate: *"Bambu is the norm, Snapmaker is the exception"* | `.fleet/notes-archive.md:1297-1301` |

### 6.3 Documented-but-unimplemented Paxx/Klipper surface

All of the following is specified in `.fleet/research-notes.md` (K-054/K-055 research) and has
**zero implementation** in `internal/printers/snapmaker/`:

- **Filament feed architecture** (`research-notes.md:179-183`): *"4 toolheads, 2 feeder modules
  (left/right), 2 channels each. Left feeder → channels 0,1 → extruders T0,T1. Right feeder →
  channels 2,3 → extruders T2,T3. Motorized DC feeders with RFID recognition (FM175XX reader).
  Klipper config sections: `[filament_feed left]`, `[filament_feed right]`."*
- **Load/unload G-code** (`:185-198`):

  ```
  FEED_AUTO MODULE=left CHANNEL=0 LOAD=1     ← load on left feeder, channel 0
  FEED_AUTO MODULE=right CHANNEL=1 LOAD=1    ← load on right feeder, channel 1
  ```
  ```
  FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1                    ← full auto unload
  FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1 STAGE=prepare      ← stage 1: home+heat
  FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1 STAGE=doing        ← stage 2: retract
  FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1 STAGE=cancel       ← cancel mid-unload
  ```
  *"Internally executes: prepare → home X/Y → pick toolhead (T<n>) → feed to nozzle → heat →
  extrude/purge → flush → finish"* (`:190`).
- **RFID / filament-detect objects** (`:200-212`): `GET /printer/objects/query?filament_feed`
  → per-extruder `channel_state` (`load_finish`, `wait_insert`, `load_feeding`, …),
  `channel_error` (`ok`, `no_filament`, `timeout`, …), `filament_detected`, `disable_auto`;
  `GET /printer/objects/query?filament_detect` → per-channel `MAIN_TYPE` (PLA/PETG/ABS/…),
  `SUB_TYPE`, `ARGB_COLOR`, `HOTEND_MIN_TEMP`, `HOTEND_MAX_TEMP`, `BED_TEMP`, `VENDOR`, `OFFICIAL`.
- `channel_state` state machine (`:214-218`) and error codes (`:220-222`):
  `ok`, `no_filament`, `residual_filament`, `motor_speed`, `wheel_speed`, `timeout`, `move`,
  `move_home`, `move_switch`, `move_extrude`, `custom_gcode`, `heat`.
- Filament load temps from firmware defaults (`:224-226`): PLA 250 °C load / 170 °C clean;
  PETG 270/205; ABS 280/220; PA-CF 300/240; PC 300/220; TPU 250/190.
- Runout detection via `filament_motion_sensor` objects (`:100`); RFID write via
  `FILAMENT_DT_QUERY/UPDATE/CLEAR` (`:102`).
- Per-toolhead temp targeting `M104 S{temp} T0`…`T3` (`:54`), bed `M140 S{temp}` (`:28`),
  fan `M106 S{0-255}` (`:35`).
- WS JSON-RPC G-code (`:55`).
- Reference source named at `:273`: *"Snapmaker U1 filament feed source: `filament_feed.py` in
  Snapmaker Klipper fork"* — i.e. Paxx/Snapmaker ship a **forked Klipper** with custom
  `filament_feed`/`filament_detect` Klipper objects, which is precisely why stock Moonraker
  tooling can't see this data.
- Related open cards: K-032 (filament loaded status per tool), K-082 (AMS load/unload commands).

---

## 7. Error handling: exact strings, timeouts, retries

### 7.1 Error strings produced by the driver (verbatim format strings)

Status/monitoring path:
- `initial status: %v` (`snapmaker.go:141`) → surfaced as `PrinterStatus.ErrorMsg`.
- `status poll failed: %v` (`snapmaker.go:203`) → surfaced as `ErrorMsg`.
- `query poll failed (non-fatal): %v` (`snapmaker.go:214`) → **log only**, never surfaced.
- `building request: %w`, `http request: %w`, `HTTP %d: %s`, `reading response: %w`,
  `parsing response: %w` (`snapmaker.go:352`, `:358`, `:364`, `:369`, `:374` — and the identical
  set in `fetchQueryStatus` at `:385`, `:391`, `:397`, `:402`, `:407`).

WebSocket path:
- `ws dial: %w` (`:242`), `ws set read deadline: %w` (`:263`), `ws read: %w` (`:307`),
  `ws read deadline: %w` (`:323`). These reach the main loop only as `msg.err`, which merely
  restarts the poll/retry timers — **they are never surfaced to the UI** and never logged.

Commands:
- `snapmaker %s: building %s request: %w` / `snapmaker %s: %s request failed: %w` /
  `snapmaker %s: %s returned HTTP %d: %s` (`doCommand`, `:578`, `:584`, `:590`) — where the
  second `%s` is `pause` | `resume` | `cancel`.
- `snapmaker %s: building skip request: %w` / `snapmaker %s: skip request failed: %w` /
  `snapmaker %s: skip returned HTTP %d: %s` (`SkipObject`, `:615`, `:621`, `:627`).
- `snapmaker %s: marshalling gcode: %w` / `snapmaker %s: building gcode request: %w` /
  `snapmaker %s: gcode request failed: %w` / `snapmaker %s: gcode returned HTTP %d: %s` /
  `snapmaker %s: gcode failed: %w` (`sendGCode`, `:705`, `:710`, `:717`, `:724`, `:733`).
- `moonraker error: %s` (`parser.go:169`) — wrapped, so the user sees e.g.
  `snapmaker u1: gcode failed: moonraker error: Klippy Not Connected`.
- `snapmaker %s: set bed temp not implemented` / `…set nozzle temp not implemented` /
  `…set chamber temp not implemented` (`:634`, `:639`, `:644`).

Exact strings asserted in tests:
- `snapmaker test-u1: pause returned HTTP 500: Internal Server Error` (`snapmaker_test.go:363`)
- `snapmaker test-u1: pause returned HTTP 401: Unauthorized` (`snapmaker_test.go:380`)
- contains `Klippy Not Connected` (`snapmaker_test.go:753-755`)

### 7.2 How they reach the UI

`writeError(w, 500, err.Error())` in the command handlers (e.g. `server.go:913-916` for light,
`:996` for home, `:1086-1088` for jog, `:794-796` for skip) → frontend `alert(d.error ||
'Command failed')` (`onboarding.go:2012`, `:2039`, `:2121`), with `'Network error'` on fetch
rejection. 409 from `requireIdleAndOnline` reads as a plain alert: `server.go:959-967` produces
`printer is offline` / `printer is not idle (state: "…")`, and `onboarding.go:2018-2023` notes
*"the backend responds 409 if the printer isn't idle/online right now … which reads as a plain
'Command failed: printer is not idle …' alert here … the UI doesn't try to distinguish 409 from
other failures since the message text already explains it."*
`PrinterStatus.ErrorMsg` (`interface.go:36`, `omitempty`) renders as the red error banner.

### 7.3 Timeout / interval summary

| Duration | Value | Where |
|---|---|---|
| REST status/command client timeout | **10 s** | `snapmaker.go:110-112` |
| G-code client timeout | **120 s** | `snapmaker.go:113-115` |
| REST poll interval | **3 s** | `:27`, always-on `:163` |
| WS reconnect interval | **15 s** | `:28`, `:167` |
| WS ping interval | **30 s** | `:29`, `:278` |
| WS write deadline (pings) | **10 s** | `:30`, `:283` |
| WS read deadline | **10 s** | `:31`, `:261`, `:271`, `:321` |
| WS handshake timeout | **5 s** | `:235` |
| `StatusCh` buffer | **16** | `server.go:231` |
| `wsCh` buffer | **4** | `snapmaker.go:148` |
| Server shutdown timeout (context) | 10 s | `server.go:301` |

### 7.4 Retries

**There are none.** No retry loop, no backoff, no `for attempt := range N` anywhere in the
package (grep-verified). The only resilience mechanisms are:
- the WS reconnect timer (every 15 s, forever);
- the always-on 3 s REST poll as a permanent second data path;
- `fetchQueryStatus` failure being non-fatal;
- malformed WS frames being skipped rather than fatal;
- the `complete`-latch absorbing one transient bad state report.

K-096 step 7 explicitly still asks for *"configurable timeout and retry for G-code/script
endpoint"*.

### 7.5 Status-replacement side effect

Both error paths in `Connect` (`:136-142`, `:197-204`) call `setStatus` with a **freshly
constructed** `PrinterStatus` rather than merging. A single failed poll therefore wipes
`CurrentFile`, `Progress`, all temps, `PositionZ`, `LightOn` and `Homed` from the cached
status (they are not re-populated by `setStatus`; only `handleStatusReport` re-injects
`LightOn`). The next successful report restores temps but **not** `CurrentFile` (K-091's
`!= ""` guard means it survives only if Moonraker still reports the filename — which per K-091
it does, so the wipe is the practical way the field ever clears).

---

## 8. Stale documentation to distrust

| Claim | Location | Reality |
|---|---|---|
| REST API on port **8080** | `PLAN.md:186`, `PLAN.md:213`; `config.example.yaml:78` | default **80** (`snapmaker.go:550-552`), fixed in `a28d468` |
| WebSocket endpoint **`/ws`** | `PLAN.md:199`, `PLAN.md:207`; doc comment `snapmaker.go:130` | **`/websocket`** (`snapmaker.go:342`) |
| `/api/v2/camera/stream` MJPEG | `PLAN.md:200`, `:214` | never implemented; real paths are `/webcam/stream.mjpg`, `/screen/snapshot` |
| "`marshalStatus` … used by the server in connectAllPrinters" | `snapmaker.go:512-513` | dead code, no callers |
| "`SkipObject` … with the skip G-code" | `snapmaker.go:610-611` | sends **no body at all** |
| `lightOn`/`completeIdleStreak` "guarded by mu" | `snapmaker.go:61`, `:69-70` | `completeIdleStreak` is mutated without the lock (`:438-445`) |
| `HomedAxes` "e.g. \"xyz\" when homed" | `parser.go:133` | never read; Paxx doesn't expose it (`snapmaker.go:498`) |
| `PrinterStatus.ActiveTrayID` "-1 when unknown or not applicable" | `interface.go:60-62` | Snapmaker leaves it at **0**, which reads as "AMS 0 tray 0" |

---

## 9. Interface surface implemented by this driver (`interface.go:111-164`)

`ID`, `Name`, `Connect`, `Status`, `Pause`, `Resume`, `Cancel`, `SkipObject`,
`CameraStreams`, `SetBedTemp` (error), `SetNozzleTemp` (error), `SetChamberTemp` (error),
`SetLight`, `HomeAll`, `Jog`, `SetHomed`.

`SetHomed` exists on the vendor-neutral interface **only** because of Paxx's missing
`homed_axes` (`interface.go:160-163`) — the clearest single sign of the Snapmaker exception in
an otherwise Bambu-shaped abstraction.
