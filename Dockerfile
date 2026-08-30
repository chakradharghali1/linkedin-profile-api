# Must be >= the version in go.mod, or the build fails with
# "go.mod requires go >= 1.26.3".
FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a static binary that runs on a minimal base image.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /out/linkedin-profile-api ./cmd/server

FROM alpine:3.20

# Required to verify LinkedIn's TLS certificate. Without this the runtime
# image has no CA bundle and every outbound HTTPS call fails with x509.
RUN apk add --no-cache ca-certificates tzdata

# Run as an unprivileged user rather than root.
RUN adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /out/linkedin-profile-api .

USER appuser

EXPOSE 8080

CMD ["./linkedin-profile-api"]
