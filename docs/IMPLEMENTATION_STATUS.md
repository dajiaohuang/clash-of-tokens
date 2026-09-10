# Implementation status

The authenticated `GET /admin/implementation` view joins inert catalog
metadata with the shared provider descriptor registry and the current runtime.
For each provider it reports the declared implementation, adapter factory,
configured account/source counts, catalog live-evidence flag, and counts of
runtime checks. It also exposes the provider reference and notes.

These columns have different provenance. A factory means the local adapter
contract is registered; a catalog flag is published metadata; runtime counts
come only from explicit checks of configured sources. None of these fields
implies that an account is currently authenticated or that an unconfigured
provider is reachable.
