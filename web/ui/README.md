# Threavia web client

A React application built with Vite. It is a stateless view and controller of
Core state: everything it shows comes from a snapshot plus the global event
stream, and closing it never stops agent execution.

## Why this shape

Three properties of the product decided the stack.

**One SSE stream feeds every view.** A snapshot seeds the state, a cursor takes
over, and a validation resolved on another device has to disappear here at
once. That is a shared-cache invalidation problem, so TanStack Query holds the
cache and `src/api/stream.ts` is its only other writer: one subscriber patches
the cache, every mounted view follows.

**The timeline is append-only and unbounded.** It is virtualised with TanStack
Virtual, so a session with thousands of events stays a conversation rather than
a frozen tab.

**The API contract is generated, not transcribed.** `src/api/schema.d.ts` comes
from `api/openapi.yaml` through `openapi-typescript`. A field renamed in Go
breaks this build rather than a user's screen, and `make web-generate-check`
fails if the file drifts from the contract.

## Development

```sh
make run-core   # terminal 1: Core on :8080
make dev-web    # terminal 2: the client on :5173, with hot reload
```

`make dev-web` runs Vite in a container that mounts this directory and shares
the host network, so it reaches Core at `localhost:8080` with no gateway or
firewall rule to arrange. Editing a component reloads the browser; the Go binary
is never rebuilt or restarted.

Everything the client calls is proxied to Core, so the browser stays on one
origin: no CORS, and the event stream behaves exactly as it does in production.
`THREAVIA_CORE_URL` points the proxy elsewhere.

Without Docker, the same thing locally:

```sh
make web-dev
```

## Production

`make web` builds into `dist/`, which `web/web.go` embeds, so a release is one
binary with no asset directory beside it. `make build` does both in order, and
the Core container image builds the client in its own stage.

A binary built without the client says so on the page rather than serving a
blank one.

## Layout

```text
src/
  api/
    schema.d.ts     generated from api/openapi.yaml, never edited
    types.ts        the names the application uses, bound to that contract
    client.ts       fetch, the channel header, and one error shape
    keys.ts         query keys, in one place so an invalidation misses nothing
    queries.ts      every query and mutation
    stream.ts       the SSE stream, and the cache it patches
  components/
    ui/             the primitives, built on Radix
    timeline.tsx    the virtualised session history
    attention.tsx   what is waiting for the user
    policy-panel.tsx what the agent may do
  routes/           draft, session and project views
```
