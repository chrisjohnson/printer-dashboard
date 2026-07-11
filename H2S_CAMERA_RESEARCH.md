# H2S Camera Protocol Research Notes

**Last Updated:** July 11, 2026
**Status:** SOLUTION FOUND

---

## TL;DR

**SOLUTION:** Enable **"LAN Only Liveview"** on the H2S printer (separate from LAN Mode). This opens port 322 for RTSPS streaming while keeping cloud connectivity for Bambu Handy. The dashboard connects via `rtsps://bblp:<access_code>@<ip>:322/streaming/live/1` through go2rtc.

---

## User Constraints

1. **LAN mode MUST remain disabled** on H2S printer (for Bambu Handy compatibility)
2. **Bambu Handy support required** (implies cloud-based camera access)
3. Solution must work without Developer Mode

## 🎯 SOLUTION FOUND: "LAN Only Liveview" (July 10, 2026)

**From ha-bambulab documentation:**
> "Camera feeds for H2D / H2S - you must enable the 'LAN Only Liveview' option in the LAN mode settings. It is not necessary to enable LAN mode generally to enable the 'LAN Only Liveview' option. This simply enables the RTSP stream to be available locally. The camera feed will also continue to work via Bambu Studio / Bambu Handy remotely."

### Key Insight

**"LAN Only Liveview"** is a **SEPARATE setting** from "LAN Mode"!

| Setting | What it does | Port 322 | Bambu Handy |
|---------|--------------|----------|-------------|
| **LAN Mode** | Full LAN access | Open | May not work |
| **LAN Only Liveview** | **Only enables RTSP stream** | **Open** | **Still works via cloud** |

### Why This Works

1. **Enabling "LAN Only Liveview"** opens port 322 for RTSPS
2. **NOT enabling LAN Mode** keeps cloud connectivity for Bambu Handy
3. **Both work simultaneously** - local RTSPS + cloud for remote access

### Action Required

Enable **"LAN Only Liveview"** on H2S printer settings WITHOUT enabling full LAN Mode.

---

## H2S Network Scan Results (nmap)

**Printer IP:** 192.168.1.17
**Serial:** 0938AC580200372
**TLS Certificate:** Issuer=BBL CA, Subject=0938AC580200372, RSA 2048-bit

### Open Ports (5 total)

| Port | Protocol | Service | Notes |
|------|----------|---------|-------|
| **8883** | TLS | MQTTS | Primary communication port. Bambu cloud MQTT connects here. |
| **990** | TLS | FTPS (implicit) | File transfers (G-code, timelapse). Requires TLS handshake first. |
| **3000** | TLS | BBL internal | Unknown service. Returns empty reply to plain HTTP. **INVESTIGATE** |
| **3002** | TLS | BBL internal | Unknown service. TLS handshake failure on malformed client hello. **INVESTIGATE** |
| **6000** | TLS | Camera/Video | Camera port. Rejects RTSP and plain HTTP. Proprietary protocol. |

### Closed/Filtered Ports

- **322** (RTSPS): CLOSED - Requires LAN mode enabled
- **80, 443, 8080, 8443, 8888, 9090**: No web UI
- **3478, 3479, 5349, 19302-19308**: No WebRTC/STUN
- **1984, 1990**: No go2rtc or SSDP

### Key Observations

1. All 5 open ports use the same BBL TLS certificate
2. No plain-text services anywhere
3. Port 6000 is TLS-encrypted and rejects standard video protocols
4. Ports 3000/3002 are **unexplored** - could be local API or camera-related

---

## Camera Protocol Matrix (All Bambu Models)

| Series | Protocol | Port | Format | LAN Mode Required |
|--------|----------|------|--------|-------------------|
| A1, A1 mini | MJPEG over TLS | 6000 | JPEG frames | No |
| P1S, P1P | MJPEG over TLS | 6000 | JPEG frames | No |
| X1, X1C, X1E | RTSPS | 322 | H.264 | Yes |
| P2S | RTSPS | 322 | H.264 | Yes |
| **H2S, H2D, H2C** | **RTSPS** | **322** | **H.264** | **Yes** |
| X2D | RTSPS | 322 | H.264 | Yes |

**Critical:** H2S is classified as "X1-class" device for camera protocol, requiring RTSPS on port 322.

---

## What We've Tried (All Failed)

### 1. Port 6000 MJPEG Protocol (A1/P1 style)
**Result:** EOF loop - connection established but immediately closed
**Why it fails:** H2S doesn't speak MJPEG protocol. It's an X1-class device.
**Evidence:** Thousands of `read frame header: EOF` errors in logs

### 2. TTCode API (TUTK P2P)
**Result:** HTTP 403 - "The specified resource is forbidden"
**Why it fails:** H2S firmware has `"tutk_server": "disable"` in ipcam config
**Evidence:**
```json
"ipcam": {
  "tutk_server": "disable",
  "brtc_service": "enable"
}
```
**API Response:**
```json
{"code":8,"error":"The specified resource is forbidden. Please check and try again."}
```

### 3. RTSPS on Port 322
**Result:** Connection refused
**Why it fails:** Port 322 is closed when LAN mode is disabled
**Evidence:** `nc -zv 192.168.1.17 322` → "Connection refused"

### 4. go2rtc RTSPS Proxy
**Result:** "stream did not become ready within 5s"
**Why it fails:** Port 322 unreachable (same as #3)

### 5. Docker --network host
**Result:** Didn't fix H2S, broke dashboard access on macOS
**Conclusion:** Docker networking is NOT the issue (P1S works fine on port 6000)

---

## What Works (For Reference)

### P1S Camera (Port 6000 MJPEG)
- Connects successfully to 192.168.1.15:6000
- Sends 80-byte auth packet (username "bblp" + access code)
- Receives 16-byte framed JPEG images
- Works perfectly in Docker

### Cloud MQTT (Both Printers)
- H2S-001 and P1S-001 both connect via cloud MQTT
- Real-time status, temperatures, progress all working
- Token authentication works

---

## Unexplored Leads (HIGH PRIORITY)

### 1. Ports 3000 and 3002
These TLS services on the H2S are completely unexplored. They could be:
- Local HTTP API (behind TLS)
- Camera WebSocket endpoint
- Bambu Connect / brtc service
- Alternative camera access method

**Action needed:** Test these ports with proper TLS handshake and various protocols.

### 2. `brtc_service: "enable"`
The H2S MQTT config shows `"brtc_service": "enable"`. This could be:
- Bambu RTC (WebRTC variant)
- Browser RTC for remote camera access
- Alternative to TUTK for cloud camera relay

**Action needed:** Research what brtc is and how to connect.

### 3. Bambu Studio Camera Connection
Bambu Studio CAN view the H2S camera. We need to understand:
- Does it use TUTK despite the "disable" flag?
- Does it connect to ports 3000/3002?
- Does it use a different cloud relay?
- What protocol does it negotiate?

**Action needed:** MITM Bambu Studio traffic to see how it connects.

### 4. Cloud Camera Proxy
The Bambu Cloud API might relay camera footage through their servers:
- TTCode returns empty `streams`, `stream_key`, `channel_name` fields
- These are "reserved for future cloud streaming features"
- Maybe there's an undocumented cloud camera relay?

**Action needed:** Check if Bambu Cloud has a camera proxy endpoint.

### 5. MQTT Camera Commands
The MQTT protocol supports camera commands:
- `camera.ipcam_timelapse` - toggles timelapse
- Could there be camera snapshot commands?
- Could there be camera stream URLs in MQTT messages?

**Action needed:** Parse MQTT messages for camera-related data.

---

## H2S Camera Config (from MQTT Report)

```json
"ipcam": {
  "agora_service": "disable",
  "brtc_service": "enable",
  "bs_state": 0,
  "ipcam_dev": "1",
  "ipcam_record": "disable",
  "laser_preview_res": 5,
  "mode_bits": 2,
  "resolution": "1080p",
  "rtsp_url": "rtsps://xxx.xxx.xxx.xxx:322/streaming/live/1",
  "timelapse": "disable",
  "tl_store_hpd_type": 2,
  "tl_store_path_type": 2,
  "tutk_server": "disable"
}
```

**Key observations:**
- `rtsp_url` is present but port 322 is closed (LAN mode disabled)
- `tutk_server: disable` - TUTK is explicitly disabled
- `brtc_service: enable` - **This is the lead to follow**
- `agora_service: disable` - Agora (another P2P service) is disabled

---

## Bambu-Lab-Cloud-API Documentation

**Repository:** https://github.com/coelacant1/Bambu-Lab-Cloud-API

### Camera Methods Documented:

1. **TUTK P2P** - Remote via proprietary protocol (requires SDK)
2. **Local JPEG Stream** - Direct TCP on port 6000 (A1/P1 only)

### TTCode API Response Fields (some empty/null):

| Field | Value for H2S | Notes |
|-------|---------------|-------|
| `ttcode` | N/A (403 error) | TUTK UID |
| `authkey` | N/A | Auth key |
| `passwd` | N/A | Camera password |
| `type` | N/A | Protocol type |
| `region` | N/A | Server region |
| `stream_key` | "" | **Empty - reserved for future** |
| `stream_salt` | "" | **Empty - reserved for future** |
| `channel_name` | "" | **Empty - reserved for future** |
| `app_id` | "" | **Empty - reserved for future** |
| `streams` | null | **Null - reserved for future** |
| `peers` | null | **Null - reserved for future** |

**The empty fields suggest Bambu is building a new cloud streaming system (possibly WebRTC-based) that isn't fully deployed yet.**

---

## OpenBambuAPI Documentation

**Repository:** https://github.com/Doridian/OpenBambuAPI

### Camera Protocols:

- **X1/P2S:** RTSPS on port 322 (`rtsps://{IP}:322/streaming/live/1`)
- **A1/P1:** MJPEG on port 6000 (proprietary framing)

### TLS Requirements:

- Self-signed certificate (CN=serial, no SAN)
- Must use `InsecureSkipVerify` or accept any cert

---

## ClusterM/open-bambu-networking Findings

**Repository:** https://github.com/ClusterM/open-bambu-networking

### Camera Protocol Details:

- **MJPEG on 6000:** 80-byte auth packet + 16-byte framed JPEG samples
- **RTSPS on 322:** Standard RTSP/RTSPS with Basic/Digest auth
- **Cloud camera (TUTK/Agora):** Proprietary, out of scope

### Port 6000 Wire Protocol (reverse-engineered May 2026):

```
TCP :6000
  └─ TLS
       └─ subchannel 0x01 login
       └─ subchannel 0x02 StartStreamEx setup + all CTRL JSON / binary
```

**This is for P2S file browser, not camera streaming.**

---

## Next Steps (Immediate)

1. **Investigate ports 3000 and 3002** on H2S
   - Try TLS handshake with BBL cert
   - Try HTTP/HTTPS requests
   - Try WebSocket upgrade
   - Try raw protocol probing

2. **Research `brtc` protocol**
   - What is Bambu RTC?
   - How does it differ from TUTK?
   - What port does it use?
   - Is there an open-source implementation?

3. **MITM Bambu Studio traffic**
   - Use Wireshark/mitmproxy to capture Studio's camera connection
   - See what endpoints it calls
   - See what protocol it negotiates

4. **Parse MQTT messages for camera data**
   - Subscribe to all H2S MQTT topics
   - Look for camera-related messages
   - Check if stream URLs appear in status reports

5. **Test port 6000 with BBL TLS auth**
   - Current code sends MJPEG auth packet
   - Maybe H2S expects different auth on port 6000?
   - Try sending MQTT-style auth or different packet format

---

## Files Modified (This Session)

- `internal/printers/bambu/client.go` - Removed TUTK code, kept RTSPS for H2S
- `internal/printers/bambu/client_test.go` - Updated test expectations
- `internal/server/server.go` - Removed TUTK pre-connect
- `internal/camera/proxy.go` - Removed tutk:// handler

---

## Docker Configuration

```bash
docker run -d \
  --name printer-dashboard \
  --restart unless-stopped \
  -p 8080:8080 \
  -v "$(pwd)/config.yaml:/app/config.yaml:rw" \
  -v "$HOME/.printer-dashboard:/home/app/.printer-dashboard:rw" \
  printer-dashboard
```

**Note:** `--network host` doesn't help (P1S works without it) and breaks dashboard access on macOS.

---

## Port 3000/3002 Investigation (July 10, 2026)

### Port 3000 - Plain TCP (NOT TLS)

| Test | Result |
|------|--------|
| TLS handshake | **FAIL** - Server actively rejects TLS ClientHello |
| Plain TCP | **Connection accepted** - No data sent back |
| HTTP GET | No response |
| WebSocket upgrade | No response |
| MQTT CONNECT | No response |
| RTSP ANNOUNCE | No response |
| All API paths | HTTP 000 (connection-level failure) |

**Conclusion:** Port 3000 is a plain TCP service that accepts connections but sends no data unprompted. It expects a custom binary protocol handshake, not HTTP/MQTT/RTSP.

### Port 3002 - TLS 1.2 Service

| Test | Result |
|------|--------|
| TLS handshake | **Succeeds** with BBL certificate |
| HTTP/HTTPS | Empty reply after TLS |
| Basic auth | TLS succeeds, empty reply |
| WebSocket | TLS succeeds, empty reply |
| Raw HTTP GET | TLS alert, connection closed |

**TLS Certificate:**
- Subject CN: `0938AC580200372` (printer serial)
- Issuer: BBL CA
- Valid: 2025-08-04 to 2035-08-02
- Key: RSA 2048-bit

**Conclusion:** Port 3002 is TLS-wrapped same service as port 3000. Expects custom binary protocol, not HTTP.

---

## Bambu Studio Camera Connection (July 10, 2026)

### Key Discovery from Forum

From https://forum.bambulab.com/t/live-video-with-h2s/213899:
- Bambu Studio CAN view H2S camera
- "Go Live" feature works for H2S
- Creates SDP file for OBS/VLC integration
- Uses ffmpeg to capture and re-stream

### From Bambu Lab Wiki

"Live View / Camera Feed Troubleshooting":
- "Your printer is in Lan Only mode. (Not supported with LAN-only mode)"
- This confirms camera works WITHOUT LAN mode when using cloud connection

### How Bambu Studio Connects

Bambu Studio uses the `libBambuSource.so` library which:
1. Connects to printer via cloud MQTT for status
2. Uses TUTK P2P for remote camera (but H2S has TUTK disabled!)
3. Falls back to local connection if on same network

**The mystery:** How does Bambu Studio view H2S camera if TUTK is disabled and LAN mode is off?

---

## brtc Protocol Investigation (July 10, 2026)

### What We Know

From H2S MQTT config:
```json
"brtc_service": "enable"
```

From web research:
- No clear documentation found for "brtc"
- It's enabled on H2S but Agora is disabled
- It's likely "Bambu RTC" - a WebRTC-based streaming service

### Hypothesis

brtc could be:
1. **Bambu RTC** - Proprietary WebRTC implementation
2. **Browser RTC** - Web-based camera access
3. **Cloud relay** - Camera stream proxied through Bambu servers

### Port Usage

brtc likely uses:
- Port 3000/3002 (the unknown TCP/TLS services we discovered)
- Or it's entirely cloud-based (no direct connection)

---

## bambu-js Library Findings (July 10, 2026)

**Repository:** https://github.com/AndrewLemons/bambu-js

### Key Details

- TypeScript/JavaScript library for Bambu printers
- Supports **P1S and H2D** models
- Has `CameraController` for frame capture
- Uses MQTT for status, custom protocol for camera

### CameraController

```typescript
const camera = CameraController.create(config);
const frame = await camera.captureFrame();
```

**This library might contain clues about H2D camera protocol since H2D is same architecture as H2S.**

---

## Open Questions (Updated)

1. **What protocol do ports 3000/3002 speak?**
   - Custom binary protocol
   - Need to capture Bambu Studio traffic to reverse-engineer

2. **What is `brtc_service` exactly?**
   - Likely Bambu RTC (WebRTC variant)
   - May be cloud-relayed, not direct connection

3. **How does Bambu Studio view H2S camera?**
   - Must be using cloud relay (since TUTK disabled and LAN mode off)
   - Need to MITM Studio traffic to see the actual connection

4. **Is there a cloud camera proxy endpoint?**
   - TTCode API has empty `streams`, `stream_key` fields
   - These are "reserved for future cloud streaming"
   - May already be implemented but undocumented

5. **Can we get camera snapshots via MQTT?**
   - MQTT supports camera commands (`camera.ipcam_timelapse`)
   - Could there be snapshot commands?
   - Could stream URLs appear in MQTT messages?

6. **Does port 6000 on H2S speak a different protocol?**
   - Current code sends A1/P1 MJPEG auth packet
   - H2S may expect different auth or protocol
   - Need to capture actual Bambu Studio traffic to port 6000

---

## Next Investigation Steps

1. **MITM Bambu Studio traffic** to see how it connects to H2S camera
2. **Try binary protocol handshakes** on ports 3000/3002
3. **Investigate bambu-js library** for H2D camera implementation
4. **Parse MQTT messages** for camera-related data
5. **Check Bambu Cloud API** for undocumented camera proxy endpoints
