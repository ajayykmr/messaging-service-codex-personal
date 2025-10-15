# syntax=docker/dockerfile:1.7

# Base build arguments shared across stages
ARG GO_VERSION=1.22
ARG SERVICE=email-worker

# ---------------------------------------------------------
# Build stage: compile the selected Go worker binary
# ---------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
ARG SERVICE
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

# Pre-download modules to leverage Docker layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the remainder of the source tree
COPY . .

# Compile a static binary for the requested service
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/${SERVICE} ./cmd/${SERVICE}

# ---------------------------------------------------------
# Runtime stage: minimal image containing only the binary
# ---------------------------------------------------------
FROM gcr.io/distroless/base-debian12 AS runtime
ARG SERVICE
WORKDIR /app

# Bring the compiled worker into the runtime image
COPY --from=build /out/${SERVICE} /app/worker

# Run as non-root for better security
USER nonroot:nonroot
ENTRYPOINT ["/app/worker"]
