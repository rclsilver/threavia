# Web client

Not implemented yet. The frontend technology is selected separately
(THREAVIA_SPEC_V1.md section 25); the minimal Session UI is step 11 of the
implementation order in section 31.

It talks to Core over HTTP/JSON and SSE only, as described in
[`../docs/api.md`](../docs/api.md). It holds no state of its own: it is a view
and a controller of Core state, and closing it never stops agent execution.
