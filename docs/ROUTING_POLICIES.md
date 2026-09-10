# Group capabilities, rates and preferences

Edit a group from **Groups → Edit** or **Routing → Edit policy**, review the
configuration diff, then apply. Policies take effect for new routing decisions.
Existing requests retain their configuration generation.

**Require vision** excludes models without declared vision support even for a
text-only request to that group. Request-level vision requirements still apply
independently. The simulator reports `vision_required` for the group exclusion.
Explicit source/model requests do not inherit a group's restrictions.

## Declared rate ceiling

Models expose optional **Input USD per million tokens** and **Output USD per
million tokens** fields. Blank means unknown; zero is an explicit declared zero
rate. These are operator-supplied values, not prices fetched or verified by the
gateway. Discovery leaves them unknown.

**Maximum USD per million tokens** in a group caps both input and output rates.
The boundary is inclusive. With a ceiling configured, either missing rate
excludes the model as `cost_unknown`; either rate above the ceiling excludes it
as `cost_ceiling`. Leaving the ceiling blank disables this rate filter. Billing
permission switches continue to apply independently: allowing unknown billing
does not bypass an explicit rate ceiling. Prices must be finite and between
zero and one billion USD per million tokens.

This is a unit-price constraint, not a per-request spending budget. It does not
estimate tokens, reserve money, include cache/tool/image prices, or guarantee a
final charge. Subscription and free-allowance sources are not assigned a zero
price automatically. A total-request budget still requires separate accounting
and output-limit work.

## Ordered preferences

For `auto`, `latency` and `load-balance`, add preferences and move them with
**Up** and **Down**. They compare currently available candidates in this order;
the first difference wins. Ties use the group's existing strategy and rotated
tie breaking. Duplicates and preferences on `weighted`, `fallback` or `select`
are rejected rather than silently changing those strategies' contracts.

| Preference | Comparison |
| --- | --- |
| `lower_latency` | Lower observed rolling request duration; unmeasured last |
| `existing_subscription` | Declared subscription billing first |
| `lower_cost` | Lower maximum of declared input/output rates; unknown last |
| `official_api` | Vendor and cloud API source kinds first |
| `reverse_source` | Browser, product, app and CLI reverse source kinds first |

For example, subscription then cost selects a subscription over a cheaper
metered source, provided both pass all eligibility and capacity checks. Reversing
the preferences reverses that priority. These declarations do not prove that a
subscription is active or that a provider is authenticated.

Provider account-pool selection happens before this comparison. Preferences do
not bypass model approval, quality, protocol, billing, shared-domain/account
capacity, cooldown, or retry exclusions. No preferences preserves the previous
strategy behavior. Request duration is not TTFT; the separate TTFT observations
remain available for diagnostics.

Tests cover actual acquisition under conflicting preferences and full capacity,
unknown rates/latency, inclusive rate boundaries, vision filtering, validation
and clone isolation. Browser regression exercises decimal rate entry, preview,
preference reordering and saved values without calling a real provider.
