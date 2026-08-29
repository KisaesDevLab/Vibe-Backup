# syntax=docker/dockerfile:1

# ---- restic, built from source ----------------------------------------------
# Building from the pinned module version gives a deterministic, checksummed
# binary (Go module sumdb) without trusting a release tarball at build time.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS restic
ARG TARGETOS
ARG TARGETARCH
ARG RESTIC_VERSION=v0.18.1
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 \
    go install -trimpath -ldflags="-s -w" \
    github.com/restic/restic/cmd/restic@$RESTIC_VERSION \
    && mkdir /out \
    && { cp "/go/bin/${TARGETOS}_${TARGETARCH}/restic" /out/restic 2>/dev/null \
         || cp /go/bin/restic /out/restic; }

# ---- vibe-backup ------------------------------------------------------------
# Cross-compile on the build platform — a Go build under QEMU emulation
# is 5-10x slower for no benefit, since Go targets arm64 natively.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ARG APP_VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
    -ldflags="-s -w -X main.version=$APP_VERSION -X main.gitSHA=$GIT_SHA -X main.buildDate=$BUILD_DATE" \
    -o /out/vibe-backup ./cmd/vibe-backup

# ---- support files for the scratch runtime ----------------------------------
# CA bundle (B2 is TLS), zoneinfo (drill schedule is firm-local time), and
# pre-owned writable directories for the non-root uid.
FROM alpine:3.22 AS support
RUN apk add --no-cache ca-certificates tzdata \
    && mkdir -p /skel/tmp /skel/var/lib/vibe-backup /skel/var/cache/vibe-backup \
    && chown -R 65532:65532 /skel

# ---- runtime ----------------------------------------------------------------
# Keep the stage name `runtime` — the release workflow targets it explicitly.
FROM scratch AS runtime
COPY --from=support /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=support /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=support /skel/ /
COPY --from=restic /out/restic /usr/local/bin/restic
COPY --from=builder /out/vibe-backup /vibe-backup
# Non-root by policy (BK-1 exit criterion). uid 65532 = conventional "nonroot".
USER 65532:65532
ENV TMPDIR=/tmp \
    RESTIC_CACHE_DIR=/var/cache/vibe-backup/restic \
    VIBE_BACKUP_STATE_DIR=/var/lib/vibe-backup
EXPOSE 4000
ENTRYPOINT ["/vibe-backup"]
CMD ["serve"]
