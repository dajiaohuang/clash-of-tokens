# Provider detail

Open a catalog provider from **Providers**. The detail view refreshes current
configuration and runtime data before displaying:

- Catalog type, adapter, protocols, implementation and supported credential/login
  checks, kept separate from configured generation evidence.
- Provider enable/Auto settings; account, source and model counts; successful and
  failed runtime request counts and their observed success rate.
- Accounts with credential references, browser authentication history, concurrent
  request limits and edit/check actions.
- Sources with routing state, configured model count and generation evidence,
  plus direct source-detail, validation and discovery actions.

The matching-generation count is the number of configured sources with at least
one matching verified model/protocol record. It is not a claim that all models,
capabilities or accounts work. Credential replacement invalidates the matching
count using the existing credential-version binding. Runtime counters include
explicit checks and are neither quota balances nor a provider-wide capacity
estimate. In particular, shared capacity is not calculated by adding source
limits.

**Explain provider eligibility** performs a read-only simulation for the selected
group and protocol with a 100-byte text request. It displays only this provider's
configured source/model results. It sends no generation request and does not
predict the final routing choice. Open **Source details** for different input
sizes or use **Routing** for tools, vision and stateful request inputs.

**Provider settings**, **Add account** and **Add source** retain the existing
preview/apply workflows. Account creation provides the import and browser-login
wizard; discovery and validation remain explicit actions against a chosen
source. Unsupported adapters report their existing unsupported-operation result.

The isolated browser regression checks configured counts, matching verification,
eligibility display, source-detail navigation and count invalidation after a
protected credential is replaced. These synthetic checks do not establish live
provider availability.
