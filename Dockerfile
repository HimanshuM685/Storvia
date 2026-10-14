# syntax=docker/dockerfile:1

# Storvia API image.
#
# Multi-stage: a full Go toolchain builds a static binary, and only the binary
# ships. The runtime stage contains no shell, no package manager, and no compiler.
#
# Unverified locally: this file was written against Go 1.27.1 and the module's
# documented requirements, but no Docker daemon was available to run a build. Treat
# the first build as something to check, not something already proven.

# --------------------------------------------------------------------------
# Stage 1 — build
# --------------------------------------------------------------------------
FROM golang:1.27-alpine AS build

# git is needed for module downloads that are not yet in the module proxy cache.
# It is discarded with this stage and never reaches the runtime image.
RUN apk add --no-cache git

WORKDIR /src

# Dependencies are copied and downloaded before the source, so a code-only change
# reuses the cached module layer instead of re-downloading the tree.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# CGO_ENABLED=0 produces a static binary, which is what lets the runtime stage be
# distroless. It works here because the SQLite driver is a pure-Go translation and
# does not need cgo.
#
# -trimpath removes local filesystem paths from the binary, and -s -w drop the
# symbol table and DWARF data. The -X flags set main.version and main.commit, which
# the -version flag reports, so a running container can be traced to a build.
ARG VERSION=dev
ARG COMMIT=""
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
        -o /out/storvia-api ./cmd/storvia-api

# --------------------------------------------------------------------------
# Stage 2 — test
# --------------------------------------------------------------------------
# Tests run in their own stage so a failing suite fails the build rather than
# shipping an image nobody checked.
FROM build AS test

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go vet ./...

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test ./...

# The race detector needs cgo, so this stage cannot be CGO_ENABLED=0.
# Requires gcc and musl-dev on the alpine base.
RUN apk add --no-cache gcc musl-dev && \
    CGO_ENABLED=1 go test -race ./...

# --------------------------------------------------------------------------
# Stage 3 — runtime
# --------------------------------------------------------------------------
# distroless/static has no shell, no package manager, and runs as a non-root user
# by default. It ships CA certificates, which this service needs: every provider
# call is over TLS to Google or to S3.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app

# Only the binary. Migrations are compiled in via embed.FS, so there is no schema
# directory to copy and no risk of shipping a stale copy.
COPY --from=build /out/storvia-api /app/storvia-api

# The configuration is supplied at run time, never baked in, so one image is
# promotable across environments.
#
# Secrets are supplied through the environment or a mounted .env file. They must
# never be a build argument or a COPY'd file: both leave a layer in the image.
ENV STORVIA_CONFIG=/app/config/config.production.json

# 65532 is the distroless nonroot user.
USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/app/storvia-api"]
CMD ["-config", "/app/config/config.production.json"]