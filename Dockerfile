# Stage 1: Download Bambu network plugin (optional — if this stage fails,
# the TUTK P2P camera feature will be unavailable but the app still works)
FROM alpine:latest AS bambu-plugin
RUN apk add --no-cache curl unzip jq
RUN mkdir -p /out && \
    RESP=$(curl -sf "https://api.bambulab.com/v1/iot-service/api/slicer/resource?slicer/plugins/cloud=02.07.00.00" \
      -H "X-BBL-Client-Type: slicer" \
      -H "X-BBL-Client-Name: BambuStudio" \
      -H "X-BBL-OS-Type: linux" 2>/dev/null) && \
    URL=$(echo "$RESP" | jq -r '.resources[] | select(.type == "slicer/plugins/cloud") | .url' 2>/dev/null) && \
    if [ -n "$URL" ] && [ "$URL" != "null" ]; then \
      curl -sfL "$URL" -o /tmp/plugin.zip && \
      unzip -o -j /tmp/plugin.zip "libBambuSource.so" "liblive555.so" -d /out && \
      echo "Bambu plugin downloaded successfully" || \
      echo "WARNING: Bambu plugin download/extract failed — TUTK camera feature unavailable"; \
    else \
      echo "WARNING: Could not find Bambu plugin download URL — TUTK camera feature unavailable"; \
    fi || echo "WARNING: Bambu plugin stage failed — TUTK camera feature unavailable"

# Stage 2: Download go2rtc pre-built binary for RTSPS camera streaming support
# (independent of source/deps, so it stays cached across app rebuilds)
FROM alpine:latest AS go2rtc
RUN apk add --no-cache curl jq
RUN mkdir -p /out && \
    GO2RTC_VERSION=$(curl -sf "https://api.github.com/repos/AlexxIT/go2rtc/releases/latest" | jq -r '.tag_name' | sed 's/^v//') && \
    curl -sfL "https://github.com/AlexxIT/go2rtc/releases/download/v${GO2RTC_VERSION}/go2rtc_linux_arm64" \
      -o /out/go2rtc && \
    chmod +x /out/go2rtc && \
    echo "go2rtc ${GO2RTC_VERSION} downloaded successfully"

# Stage 3: Build printer-dashboard
FROM golang:1.26-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Cache dependency downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build with CGo enabled
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o /app/printer-dashboard .

# Stage 4: Runtime
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata \
    libc6 libstdc++6 \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# Non-root user for security
RUN adduser --disabled-password --uid 1000 app

# Ensure HOME is set (needed for ~/.printer-dashboard/ token path)
ENV HOME=/home/app

WORKDIR /app
COPY --from=builder --chown=app:app /app/printer-dashboard .

# Copy go2rtc binary
COPY --from=go2rtc /out/go2rtc /usr/local/bin/go2rtc

# Copy Bambu network plugin (if downloaded)
COPY --from=bambu-plugin --chown=app:app /out/ /app/

# Set library path so libBambuSource.so and liblive555.so can be found
ENV LD_LIBRARY_PATH=/app:/usr/local/lib

USER app
EXPOSE 8080

ENTRYPOINT ["./printer-dashboard"]
CMD ["config.yaml"]
