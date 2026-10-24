FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./internal/dev/mcpmock ./internal/backends/shared/externalmcp && CGO_ENABLED=0 go build -trimpath -o /out/mcp-oauth-mock ./cmd/mcp-oauth-mock
FROM alpine:3.22
COPY --from=build /out/mcp-oauth-mock /usr/local/bin/mcp-oauth-mock
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/mcp-oauth-mock"]
