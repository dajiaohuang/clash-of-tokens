# Runtime execution events

**Activity → Execution events** shows the latest 1,000 released execution
attempts, newest first. Filter by source or outcome and use **Refresh history**
to fetch a new snapshot. The same data is available through the authenticated
`GET /admin/status` response as `execution_events`.

Each event contains its process-local sequence, finish time, configured source
and model IDs, protocol, validation/dispatch purpose, outcome, release status,
observed upstream HTTP status, duration and first-output duration, and a fixed
error category when available. Duration begins when the source lease is
acquired; queue time before acquisition is excluded. First-output timing is
per attempt, not the rolling average displayed in health views.

The event ring shares the router's capacity lifetime. Hot configuration updates
preserve it, including completion of requests acquired before an update.
Exactly-once lease release prevents duplicate events. The oldest entry is
replaced when the ring reaches 1,000 entries. Reads copy nested execution data,
so callers cannot mutate the retained records.

The ring is in memory and resets on process restart. It is not the persisted
validation evidence history, a durable audit log or a log of every incoming HTTP
request. Attempts rejected before source acquisition and administrative-only
leases are omitted. Retries generate separate attempt records. A canceled
execution is labeled canceled; it does not imply that upstream usage was zero.

Records never take prompts, response bodies, endpoint URLs, headers, credential
values or arbitrary adapter error text. Source/model identifiers are the
operator's configured names; do not use secret values as names. Execution errors
pass through the fixed-category redaction boundary before retention. This does
not replace the outstanding audit of every adapter's other logging paths.

Tests cover ring eviction/order, duplicate release, copy isolation, malicious
adapter error text, canceled outcomes, validation classification, retained events
from old configuration leases and omitted administrative release. Browser tests
verify the four synthetic validation attempts remain visible after credential
rotation and that source/outcome filters work.
