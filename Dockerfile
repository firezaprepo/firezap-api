# syntax=docker/dockerfile:1

# ── Build ──
# Runs on the builder's own platform and cross-compiles (pure Go), so the arm64 image does not
# need CPU emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Pure Go (PostgreSQL through pgx): no C toolchain needed, static binary.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/firezap-api ./cmd/firezap-api

# ── Run ──
FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/firezaprepo/firezap-api" \
      org.opencontainers.image.description="Open-source WhatsApp API with a built-in dashboard" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/firezap-api /firezap-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/firezap-api"]
