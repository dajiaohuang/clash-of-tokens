# Owned browser processes

**Browsers → Owned processes** lists browser launches made by the current
gateway, including launches from an account setup draft that was never saved.
Each record contains a random launch ID, profile ID, PID, start/finish time and
state: `running`, `stop_requested`, `stopped` or `exited`. **Refresh processes**
reads a new snapshot; it does not inspect unrelated operating-system processes.

**Launch provider** on a configured profile opens a selected catalog provider's
HTTPS origin in that isolated profile. The profile must be enabled and valid.
An occupied debugging port is rejected; the gateway never adopts that port's
process as its own. Account Login and draft setup use the same ownership tracker.
Browser data directories are derived from the running gateway's startup browser
state root, including when a different root is pending restart.

**Check connections** probes each configured profile's loopback CDP endpoint and
shows the browser identity, page count, configured session limit, and the
number of accounts and sources bound to that profile. Bound counts are
configuration metadata; they do not claim that a page is authenticated or that
an upstream provider has capacity. Provider-specific session inventories remain
on the Sessions page when an adapter exposes them.

## Explicit stop

**Stop** opens a confirmation naming the recorded profile and PID. Only
**Stop owned process** sends the stop request. Stopping can interrupt requests
and lose unsaved browser work, while keeping the profile's files. The API is
`POST /admin/browser_profiles/processes/{launch-id}/stop` with `{"confirm":true}`;
it requires admin authentication and the existing same-origin mutation check.

The server retains the original `os.Process` handle from `exec.Cmd.Start` and
waits for that child. It never finds a process by name, resolves a PID supplied by
the client, identifies a port owner or sends CDP `Browser.close`. Each new launch
has a new identity. A stale stop for an exited launch cannot stop a replacement
launch using the same profile. Unknown launch identities are rejected.

This controls the recorded parent process only. A browser launcher may hand off
to children and exit; an `exited` or `stopped` entry does not establish that all
browser children have exited. Such untracked processes are not terminated. CDP
connection checks and authentication evidence remain separate observations.

## Lifetime and limits

At most 64 launches may remain unfinished. One profile may have only one
unfinished owned launch. The registry retains at most 128 records and evicts the
oldest exited record when a new launch needs room. Failed starts do not create a
record. Registry reads return copied metadata.

Ownership exists only in the current gateway process. Restarting the gateway
does not reclaim earlier browser processes. Closing the gateway does not
automatically kill browsers. Deleting a profile configuration likewise does not
delete its browser data or terminate its recorded process; use the explicit
owned-process action while the gateway still retains that launch.

Tests start and reap dedicated Go test child processes, verifying process identity,
confirmation/auth/origin gates, duplicate-profile and capacity limits, failed
start behavior, historical eviction and copied timestamps. The browser regression
uses intercepted synthetic process records to test cancel/confirm behavior and
does not terminate its browser. Real Chrome, Edge, Brave, Firefox, Opera,
Vivaldi, Chromium and Arc child-process behavior across all operating systems
remains a separate compatibility verification.
