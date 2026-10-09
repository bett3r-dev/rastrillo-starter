# ADR 0004: real-time is SSE as a doorbell, with the change id as the cursor

Status: proposed. Carried into this project by rastrillo-starter from
gastos ADR 0006 ("bringing Blueprint onto rastrillo", proposed
2026-10-09), which names this as the pattern for server-to-client push
that any app here uses. Nothing here is built yet, in gastos or in
this starter; build it in the first app that needs push, then move
what two apps share into `libs/`.

## Decision

rastrillo has one shipped server-push pattern, the jobs status stream
(`jobs/events.go`; the design is rastrillo's
`docs/superpowers/specs/2026-08-22-jobs-sse-push-design.md`). Apps here
use it:

- **SSE, not WebSocket.** Writes stay ordinary POSTs; the stream is
  one-way. Anything a client would send up a socket becomes a POST.
- **The stream never carries HTML or state.** An event says something
  changed; the client re-fetches what it already knows how to render.
  One rendering path.
- **The cursor is the change id.** It rises with every tracked write,
  so the SSE event id is the latest change id and a reconnect resumes
  from `Last-Event-ID`. One table (`changes`) is both the audit log and
  the feed.
- **No broadcast primitive** in the framework. A stream either re-reads
  a cheap snapshot on a tick (`max(id)` from `changes`, what jobs does
  once a second) or waits on an in-process notification.
- **Operating rules:** a `: ping` heartbeat every 15s; a 30s write
  deadline re-armed before every write (`http.NewResponseController`);
  a connection closes itself after 5 minutes and `EventSource`
  reconnects; the stream ends when the request context does, so a
  graceful shutdown drains in time; `data-poll` polling is the fallback
  with scripts off.

## Where it holds

- **One replica per tenant** (SQLite on local disk, the deployment
  gastos ADR 0002 proposes): an in-process notification fans a write
  out to that pod's open streams. No Redis, no `LISTEN/NOTIFY`.
- **More than one replica** works with the tick, but needs shared
  storage, and SQLite over a network filesystem is not safe. Out of
  scope.
- **CARLOS hibernation** ends long-lived connections when an instance
  sleeps; `EventSource` reconnects and resumes from `Last-Event-ID`.
- **The proxy must not buffer the stream.** With ingress-nginx, turn
  response buffering off on the events route
  (`nginx.ingress.kubernetes.io/proxy-buffering: "off"`) and keep the
  proxy read timeout above the 15s heartbeat. k3s's default Traefik
  does not buffer; test whichever proxy is used.

## The JavaScript exception

Every screen works with scripts off. A screen that cannot (a pan and
zoom canvas, say) is a JavaScript island: built separately, served by
the app with `go:embed` from its own origin, with a server-rendered
read-only fallback, and **named in that app's AGENTS.md** as the one
screen that needs JavaScript, so the exception is written down rather
than eroding the rule. The gate then needs Node for that one directory.

## Open

- Whether the doorbell becomes a `libs/` package (a handler that
  streams `max(change id)`), once two apps have built it the same way.
