# Session capabilities

`GET /admin/sessions` reports both local session metadata and a capability row
for every configured source. ChatGPT Web exposes bounded conversation metadata
with explicit expire/clear operations. Other adapters are reported as
adapter-specific and unavailable until they provide a safe session inventory;
the gateway does not infer or scrape their private session stores.
