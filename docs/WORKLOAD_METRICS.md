# Workload and memory metrics

**Overview → Workload and memory** and **Metrics** display current snapshots
from authenticated `GET /admin/status`. Refresh the page data to update them.

| Metric | Meaning |
| --- | --- |
| Global in flight / limit | Acquired source leases, including still-running requests from older configurations |
| Routing queued / limit | Waiters in the current router generation; old queued requests are woken to fail when configuration is replaced |
| Ingress occupied / limit | Occupied gateway ingress slots, including request reading/processing and response delivery |
| Reserved buffers / limit | Application buffer reservations; not a measurement of actual process memory |
| Go heap allocated | Go runtime `HeapAlloc` snapshot |
| Go runtime memory obtained | Go runtime `Sys` snapshot, not total process RSS |
| Goroutines | Current Go goroutine count |
| Gateway uptime | Time since shared gateway metrics were initialized |
| Lifetime average request rate | Counted gateway requests divided by that uptime; not a recent-window rate |

Global active and queued counts are read under the router lock. Other counters
and Go memory are sampled separately, so the entire response is not one atomic
instant. Hot updates retain the shared metrics origin, counters, ingress and
active capacity. Restart-required resource limits report the current runtime's
values until a restart applies pending configuration.

The Overview in-flight total uses global capacity. Summing current source rows
would omit held requests whose source was removed during a hot update. This is
covered by a regression that removes the source while a lease remains active,
then verifies release restores the global count to zero. Queue cancellation is
also tested.

Gateway request counters, source execution counters, and explicit validation
events have different scopes. Validation checks do not become ordinary gateway
ingress requests. A retried request can create multiple execution attempts.
These metrics do not claim provider quota balance, CPU usage, OS resident memory
or browser/device process inventory.

Source status and the Metrics page additionally sum declared input/output/total
tokens from completed execution observations. The declared-cost total includes
only sources whose complete runtime attempt history has known usage and both
configured rates; unknown or oversized responses are withheld. These are
bounded runtime observations, not provider quota or billing statements. See
[token accounting](TOKEN_ACCOUNTING.md).

API tests verify admin-only access, actual configured limits, buffer accounting
and average-rate arithmetic. Browser regression verifies the workload and memory
view alongside the existing desktop/mobile layout checks.
