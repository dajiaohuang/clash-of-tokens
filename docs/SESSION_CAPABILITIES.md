# Session capabilities

`GET /admin/sessions` reports both local session metadata and a capability row
for every configured source. ChatGPT Web, China Web, China Next (Yuanbao), Zed
Hosted, Amazon Q, and Augment expose bounded metadata for explicit
`X-COT-Session` references with expire/clear operations. Session IDs shown by
the control plane are hashes of caller keys; provider conversation IDs are
metadata only. Stateless or temporary adapters remain adapter-specific and
report an explicit unsupported capability; the gateway does not infer or scrape
their private session stores.
