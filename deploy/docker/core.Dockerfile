# Threavia Core.
#
# Nothing in Core uses cgo, so the binary is fully static. The runtime image
# carries a minimal Alpine only for git, which Skill acquisition runs.
# The web client is built first and embedded, so the image is one file and the
# deployment has no asset directory to keep in step with it.

# The client build runs on the build platform and produces plain static files,
# so it is the same bytes for every target architecture.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web

WORKDIR /web

# The lockfile first, so a source-only change does not reinstall the tree.
COPY web/ui/package.json web/ui/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ui/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first, so a source-only change does not refetch the module graph.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Overwrites whatever the build context carried, so the image always embeds the
# client this build produced rather than a stale local one.
COPY --from=web /web/dist ./web/ui/dist

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/threavia-core ./cmd/threavia-core

# Not distroless: installing a Skill from a git source runs git, and ssh for a
# git+ssh URL. The binary itself still needs nothing from the distribution.
FROM alpine:3.22

RUN apk add --no-cache ca-certificates git openssh-client \
 && addgroup -g 65532 nonroot \
 && adduser -D -u 65532 -G nonroot -h /home/nonroot nonroot

COPY --from=build /out/threavia-core /usr/local/bin/threavia-core

# 8080 is the client HTTP/JSON + SSE API, 9090 the backend control stream.
EXPOSE 8080 9090
# The chart mounts the root filesystem read-only and /tmp writable, which is
# where a Skill is cloned; git and ssh look for their configuration in HOME.
ENV HOME=/tmp
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/threavia-core"]
