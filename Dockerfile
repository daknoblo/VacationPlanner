# syntax=docker/dockerfile:1

############################
# Build stage (cross-compiles for the target arch without QEMU)
############################
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build

# Pin the toolchain to the image's Go so builds never fetch one at runtime.
ENV GOTOOLCHAIN=local
WORKDIR /src

# Cache module downloads for faster rebuilds.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Build the statically linked binary.
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/vacationplanner ./cmd/server

# Prepare a writable data directory owned by the distroless non-root user (65532).
RUN mkdir -p /data

############################
# Runtime stage (multi-arch, distroless, non-root)
############################
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /
COPY --from=build /out/vacationplanner /vacationplanner
COPY --from=build --chown=65532:65532 /data /data

ENV DB_PATH=/data/vacation.db
EXPOSE 8080
VOLUME ["/data"]
USER nonroot:nonroot

# Self-probe health check (no shell/curl in distroless).
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD ["/vacationplanner", "healthcheck"]

ENTRYPOINT ["/vacationplanner"]
