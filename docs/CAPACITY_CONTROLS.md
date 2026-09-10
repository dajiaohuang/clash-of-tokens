# Account pools and shared quota capacity

**Accounts** shows each account's current in-flight count against its configured
limit. **Account pools** groups accounts by provider and opens a transactional
editor for pool strategy, account switches, weights and concurrent-request
limits. Review the complete diff before applying. Auto approval remains a
separate account setting.

Pool strategies are round-robin, least-load, sticky and weighted. The router
first chooses an eligible account within each provider for applicable group
strategies. Explicit fallback/select source order and weighted source groups
take precedence over this account-pool choice. A weight never bypasses source,
account, shared-domain or gateway capacity.

**Health → Shared quota domains** shows each domain once, with its actual shared
counter, limit and member sources. **Edit shared quota** changes the concurrency
limit for every current source in that domain in one configuration transaction.
This editor does not rename domains or move accounts between them.

Lowering a limit does not cancel running requests. The displayed active count
can temporarily exceed the new limit; new work waits until capacity is available.
Capacity held by removed accounts/domains remains visible as retiring until the
old requests finish. It is not added once per source or model.

`GET /admin/status` includes `accounts` and `quota_domains` arrays with `id`,
`active`, `limit`, `sources` and `retired`. These are router counters, not estimates
of provider token balances or monetary quota. Refresh the page's data to obtain
a new snapshot.

Router tests cover a shared domain spanning two sources, snapshots without
mutable aliases, held capacity after resource removal, and lowering a shared
limit below current usage. The browser regression edits a provider pool and a
domain limit through preview/apply and confirms the persisted values reappear.
