FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/threavia-executor ./cmd/threavia-executor
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/threavia-executor /threavia-executor
USER 65532:65532
ENTRYPOINT ["/threavia-executor"]
