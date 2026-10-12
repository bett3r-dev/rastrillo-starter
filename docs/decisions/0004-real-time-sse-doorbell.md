# ADR 0004: real-time is SSE as a doorbell, with the change id as the cursor

Status: accepted, 2026-10-12, as built: in blueprint2's `apps/server`
(session-engine slice 12, 2026-10-09) and here in `libs/doorbell`,
which `apps/myapp` mounts. Carried into this project by
rastrillo-starter from gastos ADR 0006 ("bringing Blueprint onto
rastrillo", proposed 2026-10-09), which names this as the pattern for
server-to-client push that any app here uses. Where the build differs
from the decision below is the amendment at the end.

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

## Amendment, 2026-10-12: as built (blueprint2 slice 12, `libs/doorbell`)

`libs/doorbell` is blueprint2's `kit/bell.go` without its attach and
summon lines (blueprint2's ADR 0007, which stay that app's). It follows
the decision above: SSE, writes as POSTs, no payload, the change id as
the cursor, an in-process notification, and every operating rule
(`: ping` every 15s, a 30s write deadline re-armed per write, closed
after 5 minutes, ended with the request). `apps/myapp` rings the notes
list (`GET /notes/events`). Where it differs, and why:

- **The cursor is the scope's head**, the latest change id within the
  scope the page shows (`libs/changes`' `Trail.Within`), not the
  table's: a stream rings only for what it shows, and its
  `Last-Event-ID` still rises with every change that matters to it. A
  first connection says the head its page was rendered at with
  `?after=`, since `EventSource` sends `Last-Event-ID` only on a
  reconnect. myapp's notes list listens under a key of its own,
  rung by any change within a note.
- **The notification is after the commit.** `libs/changes` has
  `Policy.Committed`, told the scopes a transaction landed changes in
  once it commits (the hook wraps the write pool to hear it), so a
  rollback rings nothing. A ring only says "look"; the stream re-reads
  the head. An unscoped change rings nothing.
- **A graceful shutdown does not end the request context.**
  `http.Server.Shutdown` waits for requests in flight and cancels none,
  so a stream would hold the drain for its whole budget. The app closes
  its bell itself on SIGTERM (`doorbell.Draining`).
- **The client is `live.js` (`libs/doorbell`, vendored as each app's
  `static/live.js` and pinned to it by a test), not rastrillo.js's
  `data-poll-push`.** That one opens one stream per element and drops
  to timer polling for good on any error, the 5-minute close included,
  so `Last-Event-ID` never resumes. live.js holds one `EventSource` per
  page and lets it reconnect. The event name (`update`) and the
  fragment header (`Rastrillo-Fragment: 1`) are rastrillo's, so both
  clients read the same stream.
- **With scripts off there is no polling**: `data-poll` is itself
  JavaScript. A page without scripts is what it was before, current as
  of its last load.
- **Bounded**: at most 256 streams per process, a 503 with
  `Retry-After` past that.
- **One process per tenant is ADR 0005's systemd unit**, socket-
  activated and exiting when idle (an open stream is a request in
  flight, so it keeps the process up), not a pod; the reasoning in
  "Where it holds" is unchanged.
