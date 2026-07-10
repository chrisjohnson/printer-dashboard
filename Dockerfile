# Stage 1: Build
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache dependency downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/printer-dashboard .

# Stage 2: Runtime
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

# Non-root user for security
RUN adduser -D -u 1000 app

# Ensure HOME is set (needed for ~/.printer-dashboard/ token path)
ENV HOME=/home/app

WORKDIR /app
COPY --from=builder --chown=app:app /app/printer-dashboard .

USER app
EXPOSE 8080

ENTRYPOINT ["./printer-dashboard"]
CMD ["config.yaml"]
