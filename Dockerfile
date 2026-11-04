# syntax=docker/dockerfile:1

# ── Build ──
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Pure Go (PostgreSQL through pgx): no C toolchain needed, static binary.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/firezap-api ./cmd/firezap-api

# ── Run ──
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/firezap-api /firezap-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/firezap-api"]
