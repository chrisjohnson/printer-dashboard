# Bambu P1S local camera — reverse-engineered protocol findings

Source of truth (read in full): `internal/camera/bambu_stream.go` (436 L),
`internal/camera/bambu_buffer.go` (356 L), `internal/camera/bambu_stream_test.go` (148 L),
`internal/camera/bambu_buffer_test.go` (277 L). Cross-file citations are marked *(xref)*.

## 1. The `bambus://` URL scheme

Canonical form (doc `bambu_stream.go:244`): `Expected query parameter: url (bambus://host:port?token=xxx)`
Producer *(xref `internal/printers/bambu/client.go:224-229`)*:
`// P1S, A1 series use bambus:// binary TLS protocol on port 6000.` →
`fmt.Sprintf("bambus://%s:6000?token=%s", c.cfg.Host, c.cfg.AccessCode)` with `Type: "internal"`, `Label: "Camera"`.

- Scheme is exactly `bambus` (TLS binary, not HTTP). X1 series instead use
  `rtsps://bblp:<token>@<host>:322/streaming/live/1` (`client.go:216-222`,
  `// RTSPS stream on port 322 (requires LAN mode enabled on printer).`).
- Credential is the **`token` query param only** — no userinfo, no username param
  (contrast with rtsps:// which puts `bblp:token@` in userinfo). `token` == Bambu LAN access code.
- Parsing, in `BambuCameraHandler` (`bambu_stream.go:253-296`), in order:
  1. `rawURL := r.URL.Query().Get("url")` — the bambus URL is itself a query value of the
     dashboard's own HTTP endpoint, so it arrives percent-encoded
     (`:255`; empty → 400 `missing url parameter`).
  2. `parsedURL, err := url.Parse(rawURL)`; `if err != nil || parsedURL.Scheme != "bambus"` →
     400 `invalid bambus url` (`:261-265`).
  3. `host := parsedURL.Hostname()`; empty → 400 `missing host in bambus url` (`:267-272`).
  4. `portStr := parsedURL.Port()`; **default `"6000"` when absent** (`:273-275`).
  5. `strconv.Atoi`, reject `err != nil || port < 1 || port > 65535` → 400 `invalid port in bambus url` (`:277-281`).
  6. `accessCode := parsedURL.Query().Get("token")`; empty → 400 `missing token in bambus url` (`:283-287`).
  7. `mgr != nil` → `mgr.GetBuffer(host, port, accessCode)` + `serveFromBuffer`; else
     `serveOnDemand` (`:289-294`).
- Dispatch by scheme happens upstream *(xref `internal/camera/proxy.go:81-83`:
  `// Dispatch by URL scheme: bambus:// uses the Bambu Lab binary TLS protocol.`)*.
- The same host/port-default-6000/token parse is duplicated in three places
  *(xref `proxy.go:353,368`, `internal/server/server.go:1188,1198`)* — no shared parser.
- Path component is ignored entirely (a trailing `/` before `?` is fine; test fixture
  *(xref `proxy_test.go:612`)* uses `bambus://192.168.1.100:6000/?token=test123`).

## 2. Binary auth packet — exact byte layout

Protocol header comment (`bambu_stream.go:26-32`), verbatim:

```
// Auth packet (80 bytes total):
//   Bytes  0-3:  uint32 LE 0x40 (64)
//   Bytes  4-7:  uint32 LE 0x3000 (12288)
//   Bytes  8-11: uint32 LE 0
//   Bytes 12-15: uint32 LE 0
//   Bytes 16-47: username "bblp" padded with nulls to 32 chars
//   Bytes 48-79: access code padded with nulls to 32 chars
```

Builder (`bambu_stream.go:67-89`), verbatim:

```go
func BuildAuthPacket(accessCode string) [bambuAuthPacketSize]byte {
	var pkt [bambuAuthPacketSize]byte

	// Bytes 0-3: uint32 LE 0x40 (64)
	binary.LittleEndian.PutUint32(pkt[0:4], 0x40)

	// Bytes 4-7: uint32 LE 0x3000 (12288)
	binary.LittleEndian.PutUint32(pkt[4:8], 0x3000)

	// Bytes 8-11: uint32 LE 0
	binary.LittleEndian.PutUint32(pkt[8:12], 0)

	// Bytes 12-15: uint32 LE 0
	binary.LittleEndian.PutUint32(pkt[12:16], 0)

	// Bytes 16-47: username "bblp" padded with nulls to 32 chars
	copy(pkt[16:48], bambuUsername)

	// Bytes 48-79: access code padded with nulls to 32 chars
	copy(pkt[48:80], accessCode)

	return pkt
}
```

Constants (`bambu_stream.go:45-58`): `bambuAuthPacketSize = 80`, `bambuUsername = "bblp"`,
`bambuFrameHeaderSize = 16`, `bambuReadTimeout = 3 * time.Second`.

| Offset | Size | Value | Notes |
|---|---|---|---|
| 0–3 | 4 | `0x00000040` LE (64) | header/length-ish field; **64 ≠ 80** — equals the 64 bytes of username+access-code payload (80−16). Semantics undocumented in code. |
| 4–7 | 4 | `0x00003000` LE (12288) | constant 12 KiB; undocumented (plausibly expected max frame size). |
| 8–11 | 4 | `0` | always zero |
| 12–15 | 4 | `0` | always zero |
| 16–47 | 32 | `"bblp"` + NULs | fixed username; padding is **implicit** from Go's zero-valued `[80]byte`, not written explicitly |
| 48–79 | 32 | access code + NULs | `copy` ⇒ **silent truncation** at 32 bytes; a 32-char code leaves no NUL terminator; no length prefix anywhere |

- Sent as a single write, immediately after handshake, no response awaited
  (`bambu_stream.go:148-153`): `pkt := BuildAuthPacket(accessCode)` / `r.conn.Write(pkt[:])`
  → error wrapped as `bambu camera: send auth: %w`.
- Wire bytes for `token=SECRET` (offsets 0x00, 0x10, 0x20, 0x30 rows):
  `40 00 00 00 00 30 00 00 00 00 00 00 00 00 00 00` / `62 62 6C 70 00×12` / `53 45 43 52 45 54 00×26`.
- Function is pure/deterministic; no nonce, timestamp, or session field → replay-safe to resend.

## 3. TLS specifics

- Endpoint: `net.JoinHostPort(host, strconv.Itoa(port))`, port default **6000** (`:110`, `:274`).
- Overview comment (`bambu_stream.go:20-24`), verbatim:
  `// The P1S camera on port 6000 uses a binary TLS protocol (NOT HTTP):`
  `//   1. TCP connect to printer_ip:6000` / `//   2. TLS handshake (self-signed cert, InsecureSkipVerify required)`
  / `//   3. Send 80-byte binary auth packet` / `//   4. Receive continuous JPEG frames: 16-byte header + JPEG data`
- Config (`:123-136`), verbatim:

```go
	tlsConn := tls.Client(rawConn, &tls.Config{
		InsecureSkipVerify: true,
		CipherSuites: []uint16{
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384, // required by H2S/O1S on port 6000
			// Go's default TLS 1.2 cipher suites (replicated explicitly so we can
			// include the RSA cipher above without losing forward-secrecy ciphers):
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
	})
```

- Stated reason for skipping verification (`:120-121`):
  `// TLS handshake with self-signed cert (same as MQTT client).`
- Stated reason for the explicit cipher list (`:121-122`):
  `// Note: H2S/O1S printers only support TLS_RSA_WITH_AES_256_GCM_SHA384 (a`
  `// static-RSA cipher excluded from Go's defaults), so we must list it explicitly.`
  plus `// Go's default TLS 1.2 cipher suites (replicated explicitly so we can`
  `// include the RSA cipher above without losing forward-secrecy ciphers):` (`:127-128`).
- **Not set anywhere**: `ServerName`, `RootCAs`/`ClientCAs`, `MinVersion`/`MaxVersion`,
  `NextProtos` (no ALPN), `ClientAuth`, `SessionTicketsDisabled`, `RenegotiationSupport`.
  No HTTP/2, no ALPN negotiation — raw bytes over TLS. Static-RSA suite ⇒ no forward secrecy
  on H2S/O1S.
- Handshake ordering (`:112-184`): TCP `DialContext` (with `ConnectTimeout`) →
  `tls.Client` + `HandshakeContext(ctx)` (explicit, ctx-aware, not lazy) → write 80-byte auth →
  `SetReadDeadline(now+3s)` → `io.ReadFull` 16-byte header → `SetReadDeadline(time.Time{})` →
  `io.ReadFull` payload. Failure paths: handshake error closes `rawConn` + `cancel()`
  (`:137-141`, `bambu camera: tls handshake: %w`). `Close()` = `cancel()` + `conn.Close()`;
  no `CloseNotify` (`:229-235`).

## 4. Response framing

Frame comment (`bambu_stream.go:34-42`), verbatim:

```
// Frame format:
//   16-byte header:
//     Bytes  0-3: uint32 LE payload size (length of JPEG data)
//     Bytes  4-7: 0x00 (fixed)
//     Bytes  8-11: 0x01 (fixed)
//     Bytes 12-15: 0x00 (fixed)
//   Followed by payload_size bytes of JPEG data (starts with FF D8 FF)
//
// The stream is continuous at approximately 1 FPS.
```

- Header size 16 (`bambuFrameHeaderSize`, `:53`); **payload size = `binary.LittleEndian.Uint32(header[0:4])`**
  — little-endian (`:173`, `:215`).
- Delimiting is **purely length-prefix**: read exactly 16 bytes, then exactly `payloadSize` bytes
  via `io.ReadFull` (`:209-226`). No magic, no delimiter, no checksum.
- Header bytes 4–15 are documented as fixed (`00 00 00 00 01 00 00 00`) but **never validated**
  in code — no sanity check of any kind beyond `payloadSize == 0`.
- `payloadSize == 0` is a hard error: `bambu camera: initial frame has zero payload size` (`:174-177`)
  and `bambu camera: frame has zero payload size` (`:216-218`). In manager mode this tears down
  the connection and triggers reconnect.
- **JPEG magic is never checked in this path.** `FF D8`/`FF D9` appear only in: the doc comment
  `starts with FF D8 FF` (`:40`), a test fixture (`bambu_buffer_test.go:20`), and a *different*
  code path *(xref `proxy.go:275-315`, `readFirstJPEGFromMJPEG`)* which byte-scans for `FF D8`
  then accumulates until `FF D9`: `// Found JPEG SOI marker. Read until FF D9 (EOI).` /
  `// We already have FF D8 — start collecting from there.` / `jpeg := []byte{0xFF, 0xD8}` —
  unbounded `append`, no size cap, returns `false` on any read error.
- **No resync logic and no max buffer size in the Bambu stream reader.** On desync/garbage the
  4-byte garbage prefix becomes `payloadSize` (up to 4 GiB): `make([]byte, payloadSize)` is
  attempted, then `io.ReadFull` blocks **with no deadline** (deadline was cleared at `:167`)
  until the printer drops the TCP connection; only then does `connectionLoop` reconnect.
  Recovery is therefore reconnect-only, and a huge `payloadSize` can trigger a large allocation.
- Steady-state read path (`:209-226`) allocates a fresh `[]byte` per frame. `ReadFrame` first
  drains a pre-read frame cached by the constructor (`:197-206`, field `nextFrame` `:97-99`):
  `// nextFrame holds a pre-read frame (used for the first frame which is`
  `// read during NewBambuStreamReader to verify the connection).`
- Doc/code mismatch worth noting (`:194-196`): `// The returned slice is only valid until the next`
  `// call to ReadFrame. Callers should copy the data if they need to keep it.` — the current
  implementation actually returns a freshly allocated slice each call, so the warning is stale.
- HTTP egress (both paths): boundary literal `"frame"`, `Content-Type: multipart/x-mixed-replace; boundary=frame`,
  `Cache-Control: no-cache`, `Connection: keep-alive`; each part is
  `--frame\r\nContent-Type: image/jpeg\r\nContent-Length: N\r\n\r\n<jpeg>\r\n` then `Flush()`
  (`:312-357` buffered path, `:373-432` on-demand path). Non-flushable writer →
  `log.Printf("bambu camera: response writer does not support flushing")` and return (`:317-321`, `:379-383`).
- On-demand connect failure → `502` `camera stream unreachable` (`:365-368`); buffered path
  writes nothing and returns silently on ctx cancel.

## 5. Timings, timeouts, FPS

| What | Value | Where |
|---|---|---|
| TCP dial timeout | `ConnectTimeout = 5 * time.Second` *(xref `proxy.go:19-20`)*, used as `net.Dialer{Timeout: ConnectTimeout}` | `bambu_stream.go:112-114` |
| Initial frame-header read deadline | `bambuReadTimeout = 3 * time.Second` | `:55-57`, `:157` |
| Steady-state read deadline | **none** — `SetReadDeadline(time.Time{})` clears it (`// Clear the deadline for subsequent reads (the stream is continuous).`) | `:166-170` |
| Reconnect backoff | start `1 * time.Second`, `backoff *= 2`, `maxBackoff = 30 * time.Second`, `backoff = 1 * time.Second // reset on successful connection` | `bambu_buffer.go:171-198` |
| TUTK session open timeout | `context.WithTimeout(m.ctx, 30*time.Second)`; same 1s→30s backoff | `bambu_buffer.go:300-331` |
| TLS handshake | bounded only by caller ctx (no dedicated timeout) | `:137` |
| Observed frame rate | `// The stream is continuous at approximately 1 FPS.` | `:42` |
| Single-connection limit | **not documented anywhere** — grep for `only one`/`single connection`/`FPS` across the repo finds nothing about port 6000 connection limits | — |
| Test-local timings | 2 s ctx timeout, `time.Sleep(50ms)` "Give the goroutine time to start waiting", 1 s watchdog `t.Fatal("WaitForFrame did not return after Update")`, 50 ms ctx timeout for cancellation test | `bambu_buffer_test.go:78,95,101,115,169,230` |

## 6. Verbatim "why" comments (all of them)

`bambu_stream.go`:
- `:20-24` — protocol overview quoted in §3 (binary TLS, NOT HTTP; 4 steps).
- `:55-56` — `// bambuReadTimeout is the timeout for reading the initial frame header` / `// after connecting (to verify the connection is alive).`
- `:97-99` — `// nextFrame holds a pre-read frame (used for the first frame which is` / `// read during NewBambuStreamReader to verify the connection).`
- `:102-104` — `// NewBambuStreamReader establishes a TLS connection to the Bambu camera,` / `// sends the authentication packet, and verifies the connection by reading` / `// the first frame header.`
- `:106` — `// The caller must call Close() when done to release the connection.`
- `:120-122`, `:127-128` — TLS/self-signed/H2S-O1S cipher rationale (quoted in §3).
- `:155-156` — `// Read the first frame header to verify the connection is alive.` / `// This also consumes the header so the first ReadFrame call gets the JPEG data.`
- `:166` — `// Clear the deadline for subsequent reads (the stream is continuous).`
- `:172` — `// Parse the payload size from the header so we can return the first frame.`
- `:186` — `// Store the first frame so ReadFrame can return it.`
- `:194-196` — `// The returned slice is only valid until the next call to ReadFrame.` / `// Callers should copy the data if they need to keep it.`
- `:240-242` — `// If mgr is non-nil, it uses the CameraManager's persistent connection and` / `// frame buffer for instant frame delivery. If nil, it falls back to the` / `// on-demand connection approach (original behavior).`
- `:299-301` — `// It blocks until the first frame arrives before writing any HTTP response` / `// headers, so the browser never receives an empty response that would cause` / `// a broken-image flash.`
- `:303-306` — `// Wait for first frame before sending any HTTP response.` / `// This blocks until either a frame arrives (from the CameraManager's` / `// background connection) or the client disconnects. The browser sees` / `// a pending HTTP request — never a zero-byte multipart response.`
- `:323` — `// Write the first frame immediately (headers + data atomically).`
- `:309`, `:339`, `:396-397` — `// client disconnected or context cancelled before first frame`, `// client disconnected`, `// Client disconnected, not a real error.`
- `:361-362` — `// serveOnDemand is the original behavior: connect to the camera on each` / `// request and stream frames directly.`

`bambu_buffer.go`:
- `:15-18` — `// FrameBuffer stores the latest JPEG frame from a Bambu camera and` / `// notifies waiting consumers using a channel-close-and-replace pattern.` / `// Each Update closes the old notify channel and creates a new one,` / `// waking all goroutines blocked in WaitForFrame.`
- `:23-24` — `notify chan struct{} // closed + replaced on each Update to wake waiters` / `closed bool         // set by Close(), prevents re-notification loop`
- `:128-130` — `// NewCameraManager creates a new CameraManager. The provided context` / `// controls the lifetime of all background connections — when it is` / `// cancelled, all connection loops exit.`
- `:141-143` — `// GetBuffer returns a FrameBuffer for the given camera, starting a` / `// background connection if one isn't already running. The key is` / `// "host:port" which uniquely identifies the camera endpoint.`
- `:168-169` — `// connectionLoop maintains a persistent connection to the camera.` / `// It reconnects with exponential backoff on failure.`
- `:198`, `:331` — `backoff = 1 * time.Second // reset on successful connection`
- `:210`, `:350` — `break // exit inner loop to reconnect`
- `:239` — `// If a session already exists for this serial, returns the existing buffer (idempotent).`
- `:26-24` region: `// errBufferClosed is returned by WaitForFrame when the buffer is closed.` (`:27`)
- `:94-95` — `// Close releases any blocked waiters and prevents future WaitForFrame calls` / `// from blocking. After Close, WaitForFrame returns errBufferClosed.`

*(xref, why-comments about this protocol elsewhere)*
- `internal/printers/bambu/client_test.go:2988-2991` — `// X1-series printers (BL-P001 = X1 Carbon, etc.) must route to RTSPS` / `// on port 322, NOT to the bambus:// protocol on port 6000 used by P1/A1.` / `// This is the fix for issue #21: X1 series cameras were incorrectly` / `// sent to port 6000, causing a tight reconnect loop.`
- `internal/camera/proxy.go:253-256` — placeholder fallback `// falling back to the gray placeholder if no frame has ever been cached for` / `// this stream (first request, or camera never reachable).`

## 7. Test fixtures and assertions

`bambu_stream_test.go` — **no network/IO tests at all**; 8 tests, all pure `BuildAuthPacket` byte-layout:
- `TestBuildAuthPacket_Size` (`:8-13`): `len(BuildAuthPacket("1234")) == 80`.
- `TestBuildAuthPacket_HeaderFields` (`:15-37`): LE u32 at 0/4/8/12 == `0x40`, `0x3000`, `0`, `0`.
- `TestBuildAuthPacket_Username` (`:39-53`): `string(pkt[16:20]) == "bblp"`; every byte 20..47 == `0x00`.
- `TestBuildAuthPacket_AccessCode` (`:55-71`): code `"ABCD1234"` at `pkt[48:56]`; bytes 56..79 all `0x00`.
- `TestBuildAuthPacket_AccessCodeTruncation` (`:73-87`): `longCode := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789" // 36 chars` → field is exactly 32 bytes and equals `longCode[:32]` (documents silent truncation as intended behavior).
- `TestBuildAuthPacket_EmptyAccessCode` (`:89-98`): empty token ⇒ bytes 48..79 all `0x00`.
- `TestBuildAuthPacket_Deterministic` (`:100-109`): two builds of `"test-code"` byte-identical over all 80.
- `TestBuildAuthPacket_AllFields` (`:111-148`): full-packet check for `"SECRET"` — header fields, `pkt[16:20]=="bblp"`, 20..47 zero, `pkt[48:54]=="SECRET"`, 54..79 zero.

`bambu_buffer_test.go` — literal byte fixtures (fake JPEGs) + concurrency semantics:
- `:20` `frame := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}` — the only JPEG-ish fixture (SOI + APP0/JFIF marker + length 0x0010); asserts `Update`→`Latest` round-trips byte-for-byte and length matches.
- `:39,46-49` `{0x01,0x02,0x03}`: two `Latest()` calls must not share a backing array (`// Modify got1 and verify got2 is unaffected.` → `Latest() returned the same backing array — must return a copy`).
- `:52-63`: mutating the caller's slice after `Update` must not change the buffer (`Update() stored a reference instead of a copy`).
- `:65-74`: `Update([0x01])` then `Update([0x02,0x03])` ⇒ `Latest() == [0x02 0x03]` (latest-only, overwrite).
- `:76-111`: `WaitForFrame(ctx,0)` blocks until `Update([]byte{0xAA, 0xBB})`, returns that frame with `seq == 1`.
- `:113-125`: 50 ms ctx timeout ⇒ `WaitForFrame` returns non-nil error and `ctx.Err() != nil`.
- `:127-146`: three updates (seq 1,2,3); `WaitForFrame(ctx, 2)` returns seq 3 / `[0x03]` immediately (stale frames skipped).
- `:148-181`: 5 concurrent waiters on `lastSeq=0`, one `Update([]byte{0xFF})` ⇒ all 5 observe `seq == 1` (broadcast wake-up).
- `:183-217`: writer pushes 100 frames `[]byte{byte(i)}`; reader's observed seq only asserted `1..100` (`// Due to the broadcast pattern, it may see any seq >= 1.`) — latest-frame delivery is explicitly *not* guaranteed.
- `:219-242`: `Close()` unblocks a waiter within 1 s and yields exactly `errBufferClosed`.
- `:244-263`: `GetBuffer("192.168.1.100",6000,"secret")` twice ⇒ same `*FrameBuffer` pointer; port 6001 ⇒ different buffer (key is host:port, token ignored in key).
- `:265-277`: `GetBuffer("127.0.0.1", 1, "secret") // port 1 — won't connect`, then `Stop()`; `// If goroutines leak, the test process won't exit cleanly.`

## 8. Notable gaps / surprises

1. Frame header bytes 4–15 and JPEG `FFD8`/`FFD9` are documented but **never validated**; the only
   sanity check in the whole read path is `payloadSize != 0`.
2. No resync, no max frame size, and **no read deadline after the first frame** ⇒ a desync or a
   silent camera hangs the reader indefinitely (until TCP errors), then reconnects at 1→30 s backoff.
3. `0x40` (64) in the auth header ≠ the 80-byte packet size; `0x3000` (12288) is an unexplained constant.
4. `InsecureSkipVerify: true` is required not only for the self-signed cert but because the
   H2S/O1S firmware offers **only** `TLS_RSA_WITH_AES_256_GCM_SHA384`, which Go's defaults exclude —
   so the whole default suite list had to be re-typed by hand.
5. The manager keys buffers by `host:port` only, so a changed access code for the same endpoint
   reuses the buffer/connection started with the old token.
6. `serveFromBuffer` deliberately withholds HTTP headers until frame #1 to avoid a broken-image
   flash; `serveOnDemand` instead returns 502 on failure — two different failure UXes for one endpoint.
7. `ReadFrame`'s "slice only valid until next call" contract is stale relative to the code.
