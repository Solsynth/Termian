# syntax=docker/dockerfile:1
#
# Two images are defined here:
#   client  — the local TUI only; the same binary `go install` produces.
#   server  — the hosted SSH endpoint (`termian-server serve`), which is the
#             default target so `docker build .` builds the fleet image.
#
# The server image needs `shared/v0.2.6` published (the same prerequisite as
# Stargate's own Dockerfile). Local hosted builds use hosted/go.work instead.

# ---- client ----
FROM golang:1.27-alpine AS build-client
WORKDIR /src
ENV CGO_ENABLED=0 GOWORK=off

RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY main.go ./
COPY internal ./internal
ARG VERSION=dev
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -buildvcs=false \
      -ldflags="-s -w -X src.solsynth.dev/sosys/termian/internal/version.Version=${VERSION}" \
      -o /out/termian .

FROM scratch AS client
WORKDIR /app
COPY --from=build-client /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build-client /out/termian /app/termian
ENV XDG_STATE_HOME=/data XDG_CONFIG_HOME=/config
VOLUME /data /config
ENTRYPOINT ["/app/termian"]

# ---- server ----
FROM golang:1.27-alpine AS build-server
WORKDIR /src
ENV CGO_ENABLED=0 GOWORK=off
# -mod=mod lets the build add the shared module's sums when v0.2.6 is first
# fetched; hosted/go.sum is kept complete for every other dependency.
ENV GOFLAGS=-mod=mod

RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
COPY hosted/go.mod hosted/go.sum ./hosted/
RUN cd hosted && go mod download
COPY main.go ./
COPY internal ./internal
COPY hosted ./hosted
ARG VERSION=dev
RUN --mount=type=cache,target=/root/.cache/go-build \
    cd hosted && go build -trimpath -buildvcs=false \
      -ldflags="-s -w -X src.solsynth.dev/sosys/termian/internal/version.Version=${VERSION}" \
      -o /out/termian-server ./cmd/termian-server

FROM scratch AS server
WORKDIR /app
COPY --from=build-server /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build-server /out/termian-server /app/termian-server
ENV XDG_STATE_HOME=/data XDG_CONFIG_HOME=/config
VOLUME /data /config
EXPOSE 2222
ENTRYPOINT ["/app/termian-server"]
