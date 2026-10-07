# Threavia Core.
#
# Nothing in Core uses cgo, so the binary is fully static and the runtime image
# carries no distribution at all: no shell, no package manager, nothing to patch.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first, so a source-only change does not refetch the module graph.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/threavia-core ./cmd/threavia-core

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/threavia-core /usr/local/bin/threavia-core

# 8080 is the client HTTP/JSON + SSE API, 9090 the backend control stream.
EXPOSE 8080 9090
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/threavia-core"]
