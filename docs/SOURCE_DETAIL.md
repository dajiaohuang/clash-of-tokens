# Source detail and sampling

Select a source ID in **Sources** to open its detail view. Opening the view
refreshes current configuration and runtime status. It shows the account,
credential configuration state, source and Auto switches, source/account/shared
concurrency, group memberships, and each configured model's upstream ID,
protocols, tier/basis, tools, vision, switches and declared prices.

Runtime observations show successful/failed request counts, observed success
rate, rolling request duration, rolling time to first output, last success and
failure, last HTTP status and cooldown. No observations are labeled accordingly.
Counters include explicit validation requests and belong to the current runtime;
they are not quota balances or proof of current authentication. The separate
generation-evidence table preserves per-model/protocol verification and
historical status after configuration or credential changes.

**Explain eligibility** runs the existing read-only simulator for the selected
group, protocol and input byte count, then filters results to this source. It
models a text request and does not send an upstream prompt. Eligibility is not a
prediction of the final selected candidate. The full Routing simulator supports
the additional tool, vision and stateful request inputs.

**Edit source** opens the transactional source editor, including model metadata,
capacity, switches and memberships. **Validate source** and **Verification
details** reuse the existing explicit validation and evidence flows.

Validation evidence separates connection, authentication, request acceptance,
streaming, and completion stages and reports the measured admin round-trip
duration. A failed stage is reported without exposing provider response bodies;
an incomplete stream is never labeled verified.

## Optional benchmark

**Benchmark → Run 3 samples** sends up to three sequential validation requests
for one selected model/protocol. Each uses the standard short validation prompt.
This is an explicit usage action and may incur provider charges even when the
source is disabled for ordinary routing. It uses existing capacity, timeout,
output-size, credential and evidence safeguards.

Each successful response adds its generation result, admin round-trip duration
and evidence-save outcome to the visible table. A failed verification or request
error stops the run immediately. **Stop samples**, closing the dialog or
replacing it aborts the current browser request and prevents subsequent samples.
Canceling cannot undo usage already incurred. Starting again is a new explicit
three-sample run; completed checks remain in bounded evidence history.

The displayed duration includes the admin call, queue and generation; it is not
TTFT. Three samples are not a load test, statistical latency estimate, quality
rating or future-availability guarantee. Individual completed validation records
are persisted when storage succeeds; the aggregate timing table is session-only.

The isolated browser regression checks detail rates/evidence, eligibility output,
three successful samples and their four total history records (including the
earlier single check), stop-on-failure, explicit cancellation and close behavior.
It uses a local synthetic upstream and synthetic held requests, with no real
provider credentials or generation.
