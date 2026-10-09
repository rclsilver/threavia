# Contributing to Threavia

Setting up a development environment, running the tests and building the
binaries are described in the [README](README.md#local-development). This file
is about what a change has to carry with it to be complete.

## Before you open a change

`make verify` runs every check CI runs: formatting, tidiness, generated code,
the web client's API types, the demo, lint and tests. A change that passes it
locally passes CI, apart from the database integration tests (`make test-db`).

## Commits

- Subjects follow [Conventional Commits](https://www.conventionalcommits.org):
  `type(scope): description`, for example `feat(web): fold the steps of a
  finished job behind one line` or `fix(core): keep a superseded decision out
  of the run context`. The description says what changes for the person using
  Threavia, not which file moved.
- One concern per commit. A follow-up to a change that has not been released
  amends it rather than stacking a fix on top.

## The contract comes first

- **HTTP API.** [`api/openapi.yaml`](api/openapi.yaml) is the contract. A new
  route, field or event type is written there first, then implemented in Core,
  then `make web-generate` refreshes the client's types
  (`web/ui/src/api/schema.d.ts`). CI fails if the generated types are stale.
- **Backend protocol.** [`api/proto`](api/proto) is the contract between Core
  and backends; `make generate` refreshes `gen/`. Breaking changes are caught
  by `make proto-breaking`. [`docs/protocol.md`](docs/protocol.md) has to be
  enough to write a backend in another language, so it changes with the proto.
- **Events.** A new persistent event type is added to
  `internal/core/events/types.go`, documented in the `Event.payload`
  description of the contract, and handled in `web/ui/src/api/stream.ts` if a
  view has to update when it arrives.

## Tests

Core and backends are tested in Go next to the code they test. Behaviour that
crosses Core, the stream and a backend has an end-to-end test (see
`internal/core/api/*_test.go`). Storage changes get an integration test that
runs against PostgreSQL with `make test-db`.

## The web client

- [`PRODUCT.md`](PRODUCT.md) says who the client is for and what it must never
  do; [`DESIGN.md`](DESIGN.md) is the design system: tokens, components and the
  patterns every page reuses (rows with a `⋯` menu, a header button that makes
  a new one with `N`, confirmations on their own line, errors that say what did
  not happen and what to do). A new page uses them rather than inventing its
  own.
- Every control works on a phone: targets of 44px there, nothing that needs a
  hover to be found.
- A change is checked in a browser, on desktop and on a phone-sized window, in
  light and dark, before it is called done.

## Keep the demo current

`/demo` is the real web client running on fictitious data, with no server and
no sign-in: Core is played in the browser by
[`web/ui/src/demo/server.ts`](web/ui/src/demo/server.ts), from the data in
[`web/ui/src/demo/data.ts`](web/ui/src/demo/data.ts), and
[`web/ui/src/demo/tour.tsx`](web/ui/src/demo/tour.tsx) walks through every
feature. It is how Threavia is shown to someone who has not installed it, so a
feature missing from the demo is a feature nobody sees.

When a change adds or changes something a person can see or do:

1. **Answer its routes in the demo.** Every API call the client makes has a
   route in `server.ts`. `make web-demo-check` (in CI and in `make verify`)
   fails otherwise and lists the calls left without an answer. A route that
   cannot be played in a browser — installing from git, subscribing to push —
   answers with `unavailable(…)`, which the client shows as an explained error.
2. **Give it fictitious data.** Add to `data.ts` what makes the feature
   visible: a session in the right state, a task with dependencies, an audit
   entry of the new kind. The data stays plausible and invented: no real
   names, hosts or credentials.
3. **Make it usable.** A mutation changes the in-memory state in `server.ts`,
   so trying the feature in the demo shows its result.
4. **Show it in the tour.** A feature worth a sentence gets a step in
   `tour.tsx`, with a selector that finds its element (a `data-tour` attribute
   when nothing else is stable). Keep each step to two sentences.
5. **Look at it.** Open `/demo`, take the tour to the new step on desktop and
   on a phone-sized window, and use the feature.

The demo uses the real pages and components, never copies of them: if a
feature only works in the demo because of a demo-specific branch in a
component, the branch belongs in `server.ts` or `data.ts` instead.

## License

Contributions are licensed under the Apache License 2.0, like the rest of the
project (see [`LICENSE`](LICENSE)).
