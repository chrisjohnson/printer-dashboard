#!/usr/bin/env node
'use strict';

// A deterministic stand-in for a Snapmaker U1's HTTP surface, so the
// Playwright suite can run without — and unaffected by — the real printer.
//
// Why this exists: tests/testdata/config.test.yaml used to point `u1` at the
// real U1 on the LAN. Every dashboard test that assumes an idle printer then
// failed whenever someone was actually printing, and CI could never
// run the suite at all. The stub answers the same endpoints the driver polls
// (see internal/printers/snapmaker/snapmaker.go) with a fixed idle state, so
// the fleet the tests see is the same on every run, on every machine.
//
// Zero dependencies on purpose: it must start from a bare `node` with no
// install step, both locally and inside the CI container.
//
// Endpoints implemented (the ones the driver and the camera proxy touch):
//   GET  /api/printer                    OctoPrint-style status + temperatures
//   GET  /printer/objects/query          Moonraker-style print stats
//   POST /printer/gcode/script           jog / home (Moonraker)
//   POST /api/printer/command            skip-object (OctoPrint-style)
//   POST /api/print/{pause,resume,cancel} print actions
//   GET  /screen/snapshot                touchscreen PNG
//   GET  /webcam/stream.mjpg             MJPEG multipart stream
//
// Deliberately NOT implemented: /websocket. The driver's WS dial fails fast
// and it falls back to REST polling on a fixed 3s interval (pollInterval in
// snapmaker.go), which is the deterministic path we want the tests to take.
//
// The state is fixed and never changes: tests that need a printing or paused
// card simulate it client-side through the page's own mergeWithCache() /
// updateCard() path rather than by mutating the printer.

const http = require('node:http');
const zlib = require('node:zlib');

const HOST = process.env.STUB_HOST || '127.0.0.1';
const PORT = Number(process.env.STUB_PORT || process.argv[2] || 18081);

// --- Fixed printer state ---------------------------------------------------

// Temperatures are deliberately well away from their targets and non-round, so
// a card that renders them proves the read path worked rather than echoing a
// default. Targets are 0: an idle printer is cold, and the frontend only draws
// a target when one is set.
const STATUS = {
  temperature: {
    bed: { actual: 25.4, target: 0, offset: 0 },
    tool0: { actual: 24.8, target: 0, offset: 0 },
    tool1: { actual: 24.1, target: 0, offset: 0 },
    tool2: { actual: 23.6, target: 0, offset: 0 },
    tool3: { actual: 23.1, target: 0, offset: 0 },
  },
  state: {
    // mapMoonrakerState() turns 'Operational' + these flags into "idle", and a
    // successful report is what sets Online=true / clears Stale.
    text: 'Operational',
    flags: {
      operational: true,
      paused: false,
      printing: false,
      cancelling: false,
      pausing: false,
      error: false,
      ready: true,
      closedOrError: false,
    },
  },
};

// An empty job: no filename, no progress, homed. The frontend's movement pad
// is enabled for an idle printer, and the tests that need "unhomed" or a live
// print inject those states through updateCard() themselves.
const QUERY = {
  result: {
    status: {
      print_stats: {
        filename: '',
        print_duration: 0,
        total_duration: 0,
        state: 'standby',
        message: '',
        info: { current_layer: 0, total_layer: 0 },
      },
      virtual_sdcard: { progress: 0 },
      gcode_move: {
        homed_axes: 'xyz',
        position: [0, 0, 0, 0],
        homing_origin: [0, 0, 0],
      },
    },
  },
};

// --- PNG generation --------------------------------------------------------

// Small, valid PNGs built with node:zlib so the snapshot and MJPEG endpoints
// return something a browser actually decodes (a real image has a non-zero
// box; a 1x1 pixel would make the camera tests depend on CSS stretching).
const CRC_TABLE = (() => {
  const table = new Int32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c;
  }
  return table;
})();

function crc32(buf) {
  let c = -1;
  for (let i = 0; i < buf.length; i++) {
    c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  }
  return (c ^ -1) >>> 0;
}

function pngChunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length, 0);
  const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body), 0);
  return Buffer.concat([len, body, crc]);
}

/** A solid RGB PNG of the given size. */
function solidPng(width, height, [r, g, b]) {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // colour type: truecolour
  const stride = width * 3;
  const raw = Buffer.alloc(height * (stride + 1));
  for (let y = 0; y < height; y++) {
    const row = y * (stride + 1);
    raw[row] = 0; // filter: none
    for (let x = 0; x < width; x++) {
      const px = row + 1 + x * 3;
      raw[px] = r;
      raw[px + 1] = g;
      raw[px + 2] = b;
    }
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    pngChunk('IHDR', ihdr),
    pngChunk('IDAT', zlib.deflateSync(raw, { level: 9 })),
    pngChunk('IEND', Buffer.alloc(0)),
  ]);
}

const SNAPSHOT_PNG = solidPng(160, 120, [42, 60, 92]);

// MJPEG frames have to be JPEG, not PNG: the dashboard's frame endpoint
// (readFirstJPEGFromMJPEG in internal/camera/proxy.go) scans the multipart
// stream for the FFD8…FFD9 markers, and a PNG stream makes that scan read
// until its 10s timeout — so the camera slot never becomes visible and the
// camera tests hang. These are two solid 160x120 JPEGs (404 bytes each) so
// consecutive frames differ, the way a live feed's do. Regenerate with:
//   magick -size 160x120 xc:'#2a3c5c' -quality 5 f1.jpg && base64 -w0 f1.jpg
const MJPEG_FRAMES = [
  '/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAKBueIx4ZKCMgoy0qqC+8P//8Nzc8P//////////////////////////////////////////////////////////2wBDAaq0tPDS8P//////////////////////////////////////////////////////////////////////////////wAARCAB4AKADASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAP/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/8QAFQEBAQAAAAAAAAAAAAAAAAAAAAH/xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oADAMBAAIRAxEAPwCYCoAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/9k=',
  '/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAKBueIx4ZKCMgoy0qqC+8P//8Nzc8P//////////////////////////////////////////////////////////2wBDAaq0tPDS8P//////////////////////////////////////////////////////////////////////////////wAARCAB4AKADASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAL/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/8QAFQEBAQAAAAAAAAAAAAAAAAAAAAH/xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oADAMBAAIRAxEAPwCAFQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB/9k=',
].map((b64) => Buffer.from(b64, 'base64'));
const MJPEG_BOUNDARY = 'stubframe';
const MJPEG_INTERVAL_MS = 500;

function mjpegPart(jpeg) {
  return Buffer.concat([
    Buffer.from(
      `--${MJPEG_BOUNDARY}\r\n` +
        'Content-Type: image/jpeg\r\n' +
        `Content-Length: ${jpeg.length}\r\n\r\n`,
      'ascii',
    ),
    jpeg,
    Buffer.from('\r\n', 'ascii'),
  ]);
}

// --- HTTP plumbing ---------------------------------------------------------

function sendJson(res, status, payload) {
  const body = Buffer.from(JSON.stringify(payload));
  res.writeHead(status, {
    'Content-Type': 'application/json',
    'Content-Length': body.length,
    'Cache-Control': 'no-store',
  });
  res.end(body);
}

function sendPng(res, png) {
  res.writeHead(200, {
    'Content-Type': 'image/png',
    'Content-Length': png.length,
    'Cache-Control': 'no-store',
  });
  res.end(png);
}

function serveMjpeg(req, res) {
  res.writeHead(200, {
    'Content-Type': `multipart/x-mixed-replace; boundary=${MJPEG_BOUNDARY}`,
    'Cache-Control': 'no-cache, no-store, must-revalidate',
    Pragma: 'no-cache',
    'X-Framerate': String(Math.round(1000 / MJPEG_INTERVAL_MS)),
  });
  let n = 0;
  const write = () => {
    if (res.writableEnded || res.destroyed) return;
    res.write(mjpegPart(MJPEG_FRAMES[n % MJPEG_FRAMES.length]));
    n++;
  };
  write();
  const timer = setInterval(write, MJPEG_INTERVAL_MS);
  const stop = () => clearInterval(timer);
  res.on('close', stop);
  req.on('aborted', stop);
}

// Drain any request body before answering: the driver POSTs JSON (gcode
// scripts), and leaving the body unread keeps the connection in a state where
// a keep-alive client can stall the next request on it.
function withBodyDrained(req, fn) {
  const chunks = [];
  req.on('data', (c) => chunks.push(c));
  req.on('end', () => fn(Buffer.concat(chunks)));
  req.on('error', () => fn(Buffer.alloc(0)));
}

const server = http.createServer((req, res) => {
  // The U1 accepts an ?access_code= query param; the stub ignores auth but
  // must not 404 on it, so route on the path only.
  const path = (req.url || '/').split('?')[0];
  const method = req.method === 'HEAD' ? 'GET' : req.method;

  if (method === 'GET') {
    switch (path) {
      case '/api/printer':
        return sendJson(res, 200, STATUS);
      case '/printer/objects/query':
        return sendJson(res, 200, QUERY);
      case '/screen/snapshot':
        return sendPng(res, SNAPSHOT_PNG);
      case '/webcam/stream.mjpg':
        return serveMjpeg(req, res);
      default:
        return sendJson(res, 404, { error: 'not found', path });
    }
  }

  if (method === 'POST') {
    return withBodyDrained(req, () => {
      switch (path) {
        case '/printer/gcode/script':
          // Moonraker answers with a bare result object; anything else and
          // parseMoonrakerError() would treat the call as failed.
          return sendJson(res, 200, { result: {} });
        case '/api/print/pause':
        case '/api/print/resume':
        case '/api/print/cancel':
        case '/api/printer/command':
          return sendJson(res, 200, {});
        default:
          return sendJson(res, 404, { error: 'not found', path });
      }
    });
  }

  return sendJson(res, 405, { error: 'method not allowed', method: req.method });
});

server.listen(PORT, HOST, () => {
  if (process.env.STUB_VERBOSE) {
    process.stderr.write(`snapmaker stub listening on http://${HOST}:${PORT}\n`);
  }
});

function shutdown() {
  server.close(() => process.exit(0));
  // Do not let a lingering keep-alive (the camera stream) hold the process.
  setTimeout(() => process.exit(0), 200).unref();
}
process.on('SIGINT', shutdown);
process.on('SIGTERM', shutdown);
