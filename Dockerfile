# syntax=docker/dockerfile:1

# Cross-compile on the build platform — a Go build under QEMU emulation
# is 5-10x slower for no benefit, since Go targets arm64 natively.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ARG APP_VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
    -ldflags="-s -w -X main.version=$APP_VERSION -X main.gitSHA=$GIT_SHA -X main.buildDate=$BUILD_DATE" \
    -o /out/vibe-backup ./cmd/vibe-backup

# BK-1 will add the restic static binary and CA certificates; until then the
# runtime stage is a bare static binary. Keep the stage name `runtime` — the
# release workflow targets it explicitly, matching the suite convention.
FROM scratch AS runtime
COPY --from=builder /out/vibe-backup /vibe-backup
# Non-root by policy (BK-1 exit criterion, enforced from day one).
USER 65532:65532
EXPOSE 4000
ENTRYPOINT ["/vibe-backup"]
CMD ["serve"]
