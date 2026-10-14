# Camera / streaming subsystem — research findings

Read-only survey of `internal/camera/{go2rtc,proxy}.go` (+tests), `internal/camera/tutk/*`,
and the camera tickets; cross-checked against `internal/server/server.go`,
`internal/printers/bambu/client.go`, `internal/printers/snapmaker/snapmaker.go`,
`PLAN.md`, `Dockerfile`, `.fleet/notes.md`. Fleet: **H2S** (RTSPS:322 via go2rtc),
**X1C/BL-P001** (same RTSPS path, K-105), **P1S** (legacy binary-TLS `bambus://:6000`),
**Snapmaker U1** (plain HTTP MJPEG/PNG).

## 1. RTSPS URL form (H2S / X1)

Exact string — `internal/printers/bambu/client.go:219`:
`fmt.Sprintf("rtsps://bblp:%s@%s:322/streaming/live/1", c.cfg.AccessCode, c.cfg.Host)`
→ `rtsps://bblp:<ACCESS_CODE>@<LAN_IP>:322/streaming/live/1`. Scheme `rtsps`; username
**always `bblp`**; password = printer access code (printer LCD, Settings > Network);
host = LAN IP; port **322**; path `/streaming/live/1`. Label `"BirdsEye Camera"`,
`Type:"internal"` (`client.go:216-222`). Test literals:
`rtsps://bblp:d5a78d50@10.0.0.50:322/streaming/live/1` (`client_test.go:241`), X1
`rtsps://bblp:x1ac@10.0.0.77:322/streaming/live/1` (`client_test.go:3007`).

**Paths on real hardware:** only `/streaming/live/1` exists. `/streaming/live/2` was
guessed and **404s** — verbatim `client.go:200-205`: *"H2S-series printers only expose a
single chamber camera; a second stream was previously guessed at /streaming/live/2, but
that path 404s on real hardware (confirmed against a live H2S), so it's not offered
here."* Corroborated `PLAN.md:159-161`, `.fleet/notes.md:33-36` (*"don't re-add a second
stream without confirming via `ffprobe` against real hardware first"*). Tests assert
`want 1 (BirdsEye only — H2S has a single physical camera)`
(`client_test.go:237`, `:271`, `:367`).

**Stale comments contradicting hardware (3):** `server.go:102-104` still says pre-connect
"Only pre-connects the BirdsEye (/live/1) feed …; the Toolhead (/live/2) feed is started
lazily on first request"; `proxy.go:22-26` and `proxy.go:95-97` justify path-inclusive
stream keys by "H2-series printers expose multiple distinct camera feeds (e.g.
/streaming/live/1, /streaming/live/2)" / "H2S's /live/1 and /live/2". The *rule* (path
belongs in the key) stands; the hardware claim is wrong.

**MQTT override:** if the printer reports `camera.ipcam_url` (`parser.go:148` →
`client.go:456-457` `c.camIPCamURL`), it is used verbatim and takes priority
(`client.go:200-212`; priority test `client_test.go:376`). Config fallback needs both
`cfg.Host` and `cfg.AccessCode`, else `CameraStreams()` returns nil
(`client.go:215-234`). Requires **"LAN Only Liveview"** — a toggle separate from full LAN
mode (`notes.md:31-33`, `PLAN.md:159-160`).

## 2. Model-based routing

All three predicates in `client.go:984-1010`:
- `UsesRTSPS(model) = IsH2S(model) || IsX1Series(model)` (`:984`) — the **camera-protocol** switch.
- `IsX1Series` (`:991`): `case "BL-P001": // X1 Carbon` only.
- `IsH2S` (`:1003`): `case "H2S","H2D","H2C","H2D PRO","P2S","X2D","O1S"` — matches
  marketing names *and* cloud codes (`O1S`=H2S) (`:999-1002`); note it also swallows P2S/X2D.
- `HasChamber` is driven by **`IsH2S`, not `UsesRTSPS`**: `client.go:137`
  (`HasChamber: IsH2S(cfg.Model)`) and re-derived in `SetModel` at `:159`. So X1C gets
  RTSPS camera + `has_chamber:false`. That split *is* K-105's fix: *"Fixed by adding
  `UsesRTSPS()` predicate covering both H2 series and X1 series (BL-P001) for camera
  protocol routing; kept `IsH2S()` for H2-specific semantics (HasChamber). PR #23."*
  Verbatim rationale `client.go:981-983`: X1 *"serves RTSPS but does NOT share H2
  semantics like multi-camera or HasChamber."* Regression tests `client_test.go:2989`,
  `:3007`, `:3044-3046`.
- else-branch → `fmt.Sprintf("bambus://%s:6000?token=%s", host, accessCode)`, label
  `"Camera"` (`client.go:224-230`). Unknown/empty model **defaults to P1S `bambus://`**
  (`client_test.go:307`, `:328`).
- Same branch duplicated at startup for eager pre-connect (`server.go:99-118`): RTSPS →
  `rtspMgr.Start(context.Background(), camera.RTSPStreamKey(u), url)`; else
  `cameraMgr.GetBuffer(host, 6000, accessCode)`. Why (`server.go:91-93`): *"Pre-connect
  camera streams eagerly on startup so frames are already buffered before the first
  browser page load. This eliminates the broken-image flash when the dashboard initially
  renders."*

## 3. go2rtc subprocess (`internal/camera/go2rtc.go`)

**Binary lookup:** `NewGo2RTCManager(binaryPath, basePort)`; empty → literal `"go2rtc"`
resolved via PATH (`:74-77`); `basePort<=0` → **1984** (`:78-80`). Server uses defaults
`NewGo2RTCManager("", 0)` (`server.go:81`). Docker bundles `/usr/local/bin/go2rtc`,
version = GitHub `releases/latest`, arch `TARGETARCH` (`Dockerfile:20-30, 67`).
**Args:** exactly `exec.CommandContext(instanceCtx, m.binaryPath, "-c", configPath)`
(`:165`); stdout/stderr inherited (`:166-167`).

**Config:** YAML via `gopkg.in/yaml.v3` from `go2rtcConfig` (`:52-69`), written to
`os.CreateTemp("", "go2rtc-*.yaml")` (`:147-157`), one per instance, removed on
Stop/failure:
```yaml
streams:
  <key>:        rtsps://bblp:CODE@host:322/streaming/live/1
  <key>_mjpeg:  ffmpeg:<key>#video=mjpeg
api:  { listen: "127.0.0.1:<basePort+offset>" }
rtsp: { listen: "127.0.0.1:18554+offset" }
```
**Ports:** `offset` from a **monotonic counter `nextOffset`**, not `len(instances)`
(`:108-117`); API `port = basePort+offset`, RTSP `rtspPort = 18554+offset`. Why (`:108-113`):
*"two concurrent Start calls for different new stream keys could otherwise both read the
same len() before either is registered and collide on the same ports. rtspPort uses its
own range, well clear of go2rtc's other default ports (8554 RTSP, 8555 WebRTC), so each
instance gets its own RTSP listener too."* Offsets are never reused → ports march upward
across stop/start churn (no reuse, no wraparound guard).
**Why a per-instance RTSP listener at all** (`:62-66`): *"go2rtc's "ffmpeg:" pseudo-source
(used to transcode H264 to MJPEG) pulls the source stream back from go2rtc's own RTSP
listener, so if two instances both default to :8554 the second one's bind fails and its
ffmpeg transcode is left with no loopback URL to read."*
**ffmpeg→MJPEG hop:** camera emits H264 but go2rtc's MJPEG/snapshot endpoints need a
JPEG/RAW source, so a *virtual* stream `<key>_mjpeg` = `ffmpeg:<key>#video=mjpeg`
(`:121-132`, `:365-372`). Consumer URLs: MJPEG
`http://127.0.0.1:<port>/api/stream.mjpeg?src=<key>_mjpeg` (`:370-372`); frame
`http://127.0.0.1:<port>/api/frame.jpeg?src=<key>_mjpeg` (`:355-363`).

**Readiness/health:** `waitForReady` polls `GET /api/streams` for 200, 100 ms interval,
**10 s** ceiling, 2 s per-request timeout (`:186`, `:224-261`). On timeout/error: SIGTERM
→ 100 ms → `Kill()` → config removed (`:187-202`). `Healthy(key)` re-GETs `/api/streams`
(2 s, `:334-351`). **No supervisor** — a crashed go2rtc is never auto-restarted; recovery
is manual: `POST /api/printers/{id}/camera-reconnect` → `rtspMgr.Stop()`, next frame
request lazily re-`Start()`s (`server.go:1224-1268`, `reconnectCameraStream`
`:1255-1268`; design note `K-099…:36`). `Stop` = SIGTERM, ≤5 s wait, then `cancel()` →
`exec.CommandContext` SIGKILL, then remove config (`:266-306`); `StopAll` (`:309-322`).
**Idempotency:** existing key returns its URL (`:102-106`); after readiness it re-checks
under lock and kills the duplicate it just spawned if another goroutine won
(`:204-219`); concurrency test `go2rtc_test.go:379-417`.

**Lifecycle decoupling from the HTTP request context** — the key comment (`:26-33`):
*"rootCtx is the parent context for every spawned subprocess. It must NOT be a caller's
per-HTTP-request context: Start is called from request handlers (via r.Context()), and a
stream is meant to keep running (shared, cached by streamKey) after that one request
ends. Only rootCtx's cancellation (StopAll/app shutdown) or an explicit Stop should kill
an instance."* `instanceCtx` derives from `rootCtx`, never caller ctx (`:159-163`).
**Leak/bug:** `waitForReady(ctx,…)` at `:186` *does* use the caller's ctx, so a browser
aborting during the ≤10 s readiness window makes `Start` fail and kill the instance it
just launched → 502. Untested.

**go2rtc missing from PATH:** `cmd.Start()` fails `exec: "go2rtc": executable file not
found in $PATH` → wrapped `go2rtc: start process for stream %s: %w` (`:169-173`) →
**502** `{"error":"RTSPS camera unavailable: …"}` (`proxy.go:99-105`). Exactly K-098:
*"Initial local binary test showed 502 — go2rtc not in PATH… However, Docker image
includes go2rtc at `/usr/local/bin/go2rtc`"*; conclusion *"NOT an auth timeout. Camera is
functional."* (`K-098…:29-33`). The **501** branch (`proxy.go:89-93`, "RTSPS camera
support requires go2rtc (install with: brew install go2rtc…)") is dead in production —
`server.go:81` always allocates a non-nil manager, so a missing binary surfaces as 502,
never 501. `FrameHandler` degrades quietly instead: `rtspMgr==nil` → placeholder
(`proxy.go:380-383`).

**Error plumbing:** `go2rtcInstance.lastError` (`:44-49`) + `SetLastError`/`LastError`/
`ClearLastError` (`:376-407`), consumed by `GET /api/printers/{id}/camera-status` →
`cameraStreamStatus` (`server.go:1153-1176`: not running → "camera service not started —
click Refresh to start"; unhealthy → "camera service is unresponsive — click Refresh to
restart"; else stored error). Frontend polls every 5 s (`onboarding.go:1410-1416`,
`checkCameraStatus` `:1434-1460`).

**Tests:** `go2rtc_test.go` compiles a **fake go2rtc binary** at `TestMain` (`:22-40`,
source `:42-119`) that line-parses `listen:` from the YAML and serves `/api/streams`,
`/api/stream.mjpeg`, `/api/frame.jpeg`. Asserted: `http://127.0.0.1:9100/api/stream.mjpeg?src=test_mjpeg`
(`:209`), `…/api/frame.jpeg?src=test_mjpeg` (`:226`, `:365`); defaults `:166-177`;
missing binary `:189-197`.

## 4. MJPEG proxy & frame endpoints

**Routes** (`server.go:527-533`): `GET /api/camera/proxy`→`camera.Handler`,
`GET /api/camera/frame`→`camera.FrameHandler`, `GET /api/printers/{id}/camera-status`,
`POST /api/printers/{id}/camera-reconnect`. Both camera routes take `?url=<absolute
camera URL>`; server builds `/api/camera/proxy?url=…` (`server.go:632`), JS extracts the
raw URL back out and re-points at `/api/camera/frame?url=…` (`onboarding.go:1763-1781`).
Proxy purpose: *"same-origin proxy to avoid mixed-content issues"* (`server.go:527`).

**Auth gate: none.** Only middleware on the mux is `noCacheMiddleware` (`server.go:73-76`,
`:480-490`) setting `Cache-Control: no-cache, no-store, must-revalidate`, `Pragma:
no-cache`, `Expires: 0`. No session/token guards `/api/camera/*` — LAN-trust model. The
only gate is the printer credential in the URL: `bambus` requires `?token=` else **400**
"missing token in bambus url" (`proxy.go:360-364`, `bambu_stream.go:285-289`); RTSPS auth
is the `bblp:code@` userinfo handed to go2rtc. Bad `url` (missing/relative/unparseable) →
400 `{"error":"missing or invalid url parameter"}` (`proxy.go:69-79`, `proxy_test.go:15-73`).

**Scheme dispatch** (`proxy.go:81-146`): `bambus`→`BambuCameraHandler(mgr)`;
`rtsps`→go2rtc; `http`/`https`→plain reverse proxy; else **501** "unsupported camera URL
scheme: %s (supported: http, https, bambus, rtsps)".

**RTSPS proxy path** (`proxy.go:87-138`): `streamKey := RTSPStreamKey(parsedURL)` →
`rtspMgr.Start(r.Context(),…)` → GET go2rtc `stream.mjpeg` → copy upstream status + all
headers → `io.Copy`. Failure → 502 "RTSPS stream unreachable". Content-Type comes from
go2rtc (`multipart/x-mixed-replace; boundary=frame`), never set locally. **Header quirk:**
`w.Header().Add(key,v)` (`:127-131`, `:168-172`) *appends* to what `noCacheMiddleware`
already `Set`, so proxied responses can carry two `Cache-Control` values.

**Chunked transfer / backpressure:** no `Content-Length` on streaming paths → Go emits
`Transfer-Encoding: chunked`; `io.Copy` is synchronous so a slow browser back-pressures
go2rtc/the camera. Verbatim (`proxy.go:148-150`, `:175-179`): *"Use the request context so
client disconnect cancels the upstream request and cleans up goroutines. No additional
timeout — once connected, the stream may run indefinitely (e.g. MJPEG)."* … *"We do NOT
use a separate goroutine — writing to http.ResponseWriter after the handler returns is
undefined behaviour (nil pointer crash)."* Dial timeout `ConnectTimeout = 5*time.Second`
(`:19-20`) on every transport so unreachable cameras fail fast (`:57-59`). The Bambu MJPEG
writer (`bambu_stream.go:300-357`) instead sets `Content-Type: multipart/x-mixed-replace;
boundary=frame`, `Cache-Control: no-cache`, `Connection: keep-alive`, writes each part as
`--frame\r\nContent-Type: image/jpeg\r\nContent-Length: N\r\n\r\n<jpeg>\r\n` +
`flusher.Flush()`. Its backpressure is **frame-dropping, not queueing**: `FrameBuffer.WaitForFrame`
is latest-value+seq (`bambu_buffer.go:64-90`). `serveFromBuffer` blocks for the first
frame *before writing any headers* — *"The browser sees a pending HTTP request — never a
zero-byte multipart response"* (`bambu_stream.go:300-311`).

**FrameHandler** (`proxy.go:323-493`) — what the UI polls: one JPEG per request,
`Content-Type: image/jpeg`, `Cache-Control: no-cache`, `Content-Length` set (`writeFrame`
`:265-270`). `bambus`: `mgr.GetBuffer(...).Latest()` else placeholder (`:345-377`).
`rtsps`: `Start`→`FrameURL`→GET `/api/frame.jpeg`→`io.ReadAll` capped **10 MiB**→
`fb.Update`→serve (`:379-441`); go2rtc 404/503 recorded as *"rtsps: stream unavailable
(status %d) — access code may be expired"* (`:423-428`). `http(s)`: one GET; if
`Content-Type` starts with `multipart/x-mixed-replace` read **only the first JPEG frame**
(`:466-485`), else `io.ReadAll` ≤10 MiB. Unknown scheme / no manager → placeholder, always
**200** (`:380-383`, `:443-446`).

**Frame-boundary handling:** `readFirstJPEGFromMJPEG` (`:275-315`) byte-scans for `FF D8`
then accumulates to `FF D9`, returning `(jpeg,true)`; any read error before a complete
frame → `(nil,false)`. Safe because JPEG entropy-coded data escapes `FF` as `FF 00`, so
`FF D9` can't occur in scan data (a stray `FF D9` in a comment/APP segment would truncate
early — untested).

**Backpressure on the frame path:** the fetch deliberately ignores `r.Context()`
(`proxy.go:403-407`): *"Use a fixed timeout context (not r.Context()) so that a browser
closing/breaking the image load (which cancels r.Context) does not cause spurious
"context canceled" errors to be stored as persistent camera errors."* Paired with
`setCameraError` (`:31-49`), which drops `context.Canceled`/`DeadlineExceeded` and their
string forms because *"those are transient (browser closing the image load, request
timeout) and would cause false-positive auth error banners that keep recurring even after
refresh."*

**Stream key:** `RTSPStreamKey(u) = hostname + ":" + port + path(/→_)` (`:27-29`), e.g.
`rtsps://bblp:c@10.0.0.50:322/streaming/live/1` → `10.0.0.50:322_streaming_live_1`. Why
(`:22-26`): *"It must include the path (not just host:port): H2-series printers expose
multiple distinct camera feeds … on the same host:port, so host:port alone would collide
and silently serve one camera's stream for both."*

**K-060 flicker — root cause + fix.** Two causes: (1) fixed pre-card (K-059, commit
`95b942f`): frontend `onerror` set `display:none` on the `<img>` → black flash; now
`onerror` only makes the slot visible and the 2 s loop retries
(`K-060…:52-55`; live code `onboarding.go:1394-1408`, `:1781`). (2) **the backend cause:**
`FrameHandler` fell back to `servePlaceholder()` on *any* transient upstream failure — a
solid-gray 1×1 middle-gray JPEG (`placeholderJPEG`, `proxy.go:194-219`, "Generated with
Go's image/jpeg at Q50") served with **200 OK**; because it's a valid 200 with real JPEG
bytes the browser's `onload` fires, so it was *"indistinguishable from real flicker, and
NOT covered by the K-059 frontend fix at all"* (`K-060…:56-63`). Bambu `bambus://` never
had it — `FrameBuffer.Latest()` retains the last frame indefinitely (`K-060…:64-68`).
**Fix:** `lastGoodFrameCache` (`proxy.go:229-252`, wraps Bambu `FrameBuffer`, keyed
`"rtsps:"+streamKey` / `"http:"+rawURL`, no eviction — "the key set is bounded by the
user's configured cameras") + `serveLastGoodOrPlaceholder` (`:257-263`) on every transient
branch, with `rtspMgr==nil` and unknown-scheme left as bare placeholders (permanent
capability gaps; `proxy_test.go:166-189`, `:300-324`). Companion fix:
`readFirstJPEGFromMJPEG`'s signature became `([]byte,bool)` because it previously
*"silently returns placeholderJPEG bytes on a mid-read error, and the HTTP/HTTPS branch
(line 389) treats that return value as a *successful* frame"* — which would have
*"poison[s] the cache with placeholder bytes on every MJPEG read hiccup"*
(`K-060…:76-83`, `:186-196`). Tests `proxy_test.go:395-607` (RTSPS last-good; HTTP
last-good; MJPEG mid-frame drop via hijacked conn; first-ever failure → placeholder).

**Frontend cadence:** single-frame images every **2 s** with `&_t=<ms>` cache-buster
(`onboarding.go:1401-1407`), touchscreen every **3 s** (`:2396-2404`), camera health every
**5 s** (`:1410-1416`). Rationale (`:1770-1776`): *"/api/camera/frame returns a single
complete JPEG (or placeholder) which the browser fully decodes before firing onload, so
there is never a blank/flash rendering frame."*

## 5. TUTK (`internal/camera/tutk/`)

**Investigated, plumbed, never wired.** `PLAN.md:176-180`: *"Remote access: Uses TUTK P2P
protocol (proprietary SDK, used by Bambu Studio/Handy) … Not implementable without TUTK
SDK — users should use Bambu Handy app for remote camera"*; `PLAN.md:163-166`: *"TUTK P2P
(below) was investigated but never implemented for H2S — LAN-only was sufficient and
avoided depending on Bambu's proprietary remote-camera SDK."* Code proof:
`CameraManager.StartTUTK`/`StopTUTK`/`TUTKBuffer`/`tutkConnectionLoop`
(`bambu_buffer.go:237-356`) have **zero callers**; `BambuCloudClient.GetTTCode`
(`bambu/auth.go:607-647`) has **zero callers**; `tutk.LoadLibrary` is never called, so
`libBambuSource.so` is never loaded at runtime despite shipping.

**TTCode:** creds from Bambu Cloud `POST /v1/iot-service/api/user/ttcode`
(`tutk/tutk.go:8-9`, `auth.go:610-612`): `TTCode` (P2P UID), `AuthKey`, `Passwd`
(6-char hex), `Region` (`us`/`cn`), `Serial`. `BuildURL` (`tutk.go:30-64`) renders what
`libBambuSource.so`'s `Bambu_Create()` consumes:
`bambu:///tutk?uid=…&authkey=…&passwd=…&region=…&device=…&net_ver=01.09.03.01&dev_ver=00.00.00.00&refresh_url=1&cli_id=00000000-0000-0000-0000-000000000000&cli_ver=1.0.0`.
Defaults `:25-28`; `cli_id` is a literal placeholder UUID ("auto-generated if empty" is
documented, not implemented). Tests `tutk_test.go:9-107` (params/defaults/escaping of
space, `&`, `=`).

**libBambuSource.so / liblive555.so:** Docker **Stage 1** queries
`https://api.bambulab.com/v1/iot-service/api/slicer/resource?slicer/plugins/cloud=02.07.00.00`
then `unzip -j … "libBambuSource.so" "liblive555.so"`; every failure branch is a
non-fatal `echo "WARNING: … TUTK camera feature unavailable"` (`Dockerfile:1-17`);
copied to `/app/`, `LD_LIBRARY_PATH=/app:/usr/local/lib` (`Dockerfile:69-72`). Go search
order `defaultLibPaths`: `libBambuSource.so` (CWD/LD_LIBRARY_PATH),
`/usr/local/lib/libBambuSource.so` ("Docker bundle path"), `/app/libBambuSource.so`
(`tutk_linux_amd64.go:165-170`). Loaded via `dlopen(RTLD_LAZY|RTLD_LOCAL)` + 12 `dlsym`s
behind static C shims (`:92-135`); `Bambu_Init` once via `sync.Once` (`:209-227`). Retry
knobs (`:156-163`): `maxReadSampleRetries=50`@10 ms, `startStreamRetries=50`@100 ms,
`openTimeout=30s`. `Session.Open` runs `Bambu_Open` in a goroutine so ctx cancellation can
close the tunnel out from under it (`:250-328`).

**Build tags + the amd64 break (K-101).** `tutk_linux_amd64.go` has **no build tag** —
gated purely by the `_linux_amd64` filename suffix, and needs cgo. `tutk_stub.go` carries
`//go:build !(linux && amd64)` (`:1`) and is the fallback for *every* other platform
**including linux/arm64**. K-101: *"This is the *only* real TUTK implementation
(filename-gated `linux && amd64`); `tutk_stub.go` is the no-op fallback for every other
platform including arm64, so arm64 was never at risk from this bug."* The file had
**never compiled** before K-100's multi-arch build (Dockerfile previously hardcoded an
arm64-only go2rtc). Three distinct latent cgo bugs fixed in sequence (K-101 decision log):
(1) `info.type`→`info._type` — *"cgo renames a colliding C struct field to `_type`"*; code
comment `:320-321` *"cgo renames C struct fields that collide with Go keywords by prefixing
an underscore, so the C field "type" becomes "_type"."* (2) `C.usleep`/`C.useconds_t`
undeclared → missing `#include <unistd.h>` (now `:7`). (3) `info.format.video` invalid —
*"cgo represents unions as an opaque byte array (no named Go fields)"*; fixed by named
`Bambu_VideoFormat`/`Bambu_AudioFormat` in the preamble (`:30-34`) plus
`(*C.Bambu_VideoFormat)(unsafe.Pointer(&info.format))` (`:313-318`, comment *"cgo has no
representation for anonymous C unions, so the "format" union comes through as an opaque
byte array"*). First green multi-arch build run 30517443672; image on
`ghcr.io/chrisjohnson/printer-dashboard`.

**Stub fallback:** `ErrUnsupportedPlatform = errors.New("TUTK P2P camera requires
linux/amd64 platform with libBambuSource.so")`; `NewSession`→`(nil,err)`,
`Open`/`ReadSample`→err, `Close` is a **no-op returning nil** (`tutk_stub.go:10-35`).
`tutk_test.go:109-116` (`TestStubNewSession`) asserts the stub error — it only exercises
the stub on non-amd64; on amd64 the real `NewSession` returns `ErrLibraryNotLoaded`
(still an error, so it passes either way). `StartTUTK` logs and returns a nil buffer on
failure (`bambu_buffer.go:246-250`), so a stub platform degrades silently.

## 6. Snapmaker U1 (Paxx) camera

`snapmaker.go:762-794`, base `http://<host>:<port>`, `port` default **80**:
`/webcam/stream.mjpg` (`internal`/"Camera") and `/screen/snapshot`
(`touchscreen`/"Touchscreen"), each with optional `?access_code=<code>` appended
(`withAuth`, `:776-785`, "Follows the same pattern as Bambu's CameraStreams which includes
?token=xxx"). Fixture: `http://192.168.1.100:8080/webcam/stream.mjpg?access_code=my-access-code`
(`snapmaker_test.go:95-119`). Plain `http://` → generic branch of `Handler`/`FrameHandler`
(MJPEG first-frame extraction applies).

**Confirmed:** `/screen/snapshot` returns **PNG** — Playwright `tests/camera.test.ts:36-43`
hits `http://192.168.1.10/screen/snapshot` and asserts `expect(ct).toBe('image/png')`;
also `.research/snapmaker.md:139-140`. Interactive page is `/screen/`, reached by rewriting
the snapshot URL (`onboarding.go:1756`; code comment `snapmaker.go:789-791`). Touchscreen
frames are routed via `/api/camera/frame` "to avoid progressive rendering"
(`onboarding.go:1761-1762`). **Assumed / never implemented:** `PLAN.md:200` still lists
`/api/v2/camera/stream` (GET MJPEG) marked *"unconfirmed, likely"* — never implemented
(`.research/snapmaker.md:145-147`); `PLAN.md:186-189` hedges the transport itself
("likely exposes REST API on port 8080 (common)… API key or no auth if LAN-only") while
the shipped default is port 80 running Fluidd/Klipper (Moonraker).

## 7. Verbatim "why" comments (workarounds worth knowing)

- `go2rtc.go:26-33` rootCtx vs request ctx (quoted §3): *"Start is called from request
  handlers (via r.Context()), and a stream is meant to keep running (shared, cached by
  streamKey) after that one request ends."*
- `go2rtc.go:62-66`: *"if two instances both default to :8554 the second one's bind fails
  and its ffmpeg transcode is left with no loopback URL to read."*
- `go2rtc.go:108-113`: *"two concurrent Start calls … could otherwise both read the same
  len() before either is registered and collide on the same ports."*
- `go2rtc.go:121-124`: *"go2rtc's MJPEG endpoint requires a JPEG/RAW source; the camera's
  raw stream is H264, so declare a second virtual stream that transcodes it via ffmpeg."*
- `proxy.go:22-26`: *"host:port alone would collide and silently serve one camera's stream
  for both."*
- `proxy.go:31-34`: *"those are transient (browser closing the image load, request
  timeout) and would cause false-positive auth error banners that keep recurring even
  after refresh."*
- `proxy.go:175-179`: *"writing to http.ResponseWriter after the handler returns is
  undefined behaviour (nil pointer crash)."*
- `proxy.go:229-233`: *"It lets FrameHandler serve the last good frame on a transient
  fetch failure instead of falling back to the gray placeholder, mirroring the behavior
  FrameBuffer already gives Bambu cameras."*
- `proxy.go:450-452`: *"For MJPEG streams (multipart/x-mixed-replace), read only the first
  JPEG frame instead of consuming the entire never-ending stream."*
- `client.go:200-205` `/live/2` 404 (quoted §1); `client.go:981-983` X1 *"does NOT share
  H2 semantics like multi-camera or HasChamber."*
- `server.go:91-93`: eager pre-connect *"eliminates the broken-image flash when the
  dashboard initially renders."*
- `bambu_stream.go:300-311`: *"The browser sees a pending HTTP request — never a zero-byte
  multipart response."*
- `bambu_buffer.go:16-19`: FrameBuffer *"notifies waiting consumers using a
  channel-close-and-replace pattern."*
- `tutk_linux_amd64.go:30-32, 315-321`: union/keyword cgo workarounds (quoted §5).
- `Dockerfile:1-2`: *"if this stage fails, the TUTK P2P camera feature will be unavailable
  but the app still works."*

## 8. Open camera tickets (all empty templates)

`K-029` P1S cloud camera ("Evaluate options for P1S camera access through the cloud API
rather than direct LAN. Deferred."); `K-040` P1S camera regression test ("Verify P1S
camera still works after the K-001 refactor") — **still open**, so the whole
`bambus://:6000` P1S path is untested since the H2S refactor; `K-049` extract camera code
as a reusable package; `K-056` "Investigate low frame rate on H2S (go2rtc RTSPS) and P1S
(bambus://). Deferred." (`PLAN.md` notes the binary-TLS path is *"~1 FPS, single concurrent
connection"*); `K-016` Bambu Camera Plugin research as an alternative. None has Working
context / Decision log content — unfilled stubs, no extra findings.

**Most surprising:** (a) the 10 s readiness wait is bound to the *request* context, so a
browser abort during startup kills the instance → 502 — the one place the rootCtx
decoupling leaks; (b) the 501 "install go2rtc" branch is unreachable in production
(manager always non-nil), so a missing binary surfaces as an opaque 502 — the exact
K-098 misdiagnosis; (c) three stale comments still describe a `/streaming/live/2` toolhead
camera that real hardware 404s on; (d) the entire TUTK stack (cgo bindings, TTCode fetch,
Docker plugin download) is dead code with zero callers and had never compiled for amd64
until K-101.
