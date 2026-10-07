# Threavia Claude BackendInstance, for the Kubernetes deployment of
# specification section 29.
#
# Unlike Core, this image needs a real userland: it runs Claude Code, which the
# agent then uses to touch a filesystem and call git. Provider credentials are
# supplied at runtime and never baked in.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
# The local state uses a pure Go SQLite driver, so this stays cgo-free too.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/threavia-backend-claude ./cmd/threavia-backend-claude

FROM node:22-bookworm-slim

# Pinned at build time so an image rebuild is reproducible.
ARG CLAUDE_CODE_VERSION=latest

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git openssh-client \
 && rm -rf /var/lib/apt/lists/* \
 && npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
 && npm cache clean --force

COPY --from=build /out/threavia-backend-claude /usr/local/bin/threavia-backend-claude

# The backend keeps durable local state and the provider keeps its own session
# data, so both need a writable home. Mount a volume here.
RUN useradd --create-home --uid 65532 --shell /bin/bash threavia
USER threavia
WORKDIR /home/threavia
ENV HOME=/home/threavia
VOLUME ["/home/threavia/.threavia"]

ENTRYPOINT ["/usr/local/bin/threavia-backend-claude"]
