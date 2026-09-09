# PhindAi and WhiteRabbitNeo

These native Go implementations use protocol facts from
[gpt4free commit 502b0e2e8d82292a39d2c3d9da07125322870583](https://github.com/xtekky/gpt4free/tree/502b0e2e8d82292a39d2c3d9da07125322870583).
The reference repository is GPL-3.0; its source files were not copied into
this package. The Go request handling, validation, streaming conversion and
tests were independently written.

| Inventory ID | Adapter | Actual source | Credential | Protocol |
|---|---|---|---|---|
| `phind` | `phindai` | `https://phindai.org`, not phind.com | No account token; obtains the site's public form nonce from its own homepage | GET `/`, POST `/wp-admin/admin-ajax.php` with `action=phind_ai_send`, `nonce`, `message`; successful JSON `data.response` |
| `whiterabbitneo` | `whiterabbitneo` | `https://www.whiterabbitneo.com` | Existing own Cookie in `KeyEnv` | POST `/api/chat`, JSON body with messages, request ID, `enhancePrompt=false`, `useFunctions=false`; plain UTF-8 response |

Both require the explicit `default` model alias. Neither pinned request
has a model-selection field; reporting a selectable version would be misleading.
PhindAi accepts one user text prompt. WhiteRabbitNeo preserves text message
history. Tools, images and unsupported request fields are rejected. Only Chat
Completions is exposed.

PhindAi's upstream answer is buffered JSON; downstream streaming is explicitly
marked `X-COT-Delivery: buffered`. WhiteRabbitNeo streams text incrementally,
including UTF-8 characters split across reads. Its pinned source has no
in-band completion marker, so completion requires clean HTTP body EOF.
Transport truncation, incomplete UTF-8, non-text MIME and excessive output
fail the response. Neither implementation imports browser cookies or creates
accounts. Caller credentials are not forwarded.

Reference paths: `g4f/Provider/PhindAi.py` and
`g4f/Provider/needs_auth/WhiteRabbitNeo.py`. Contract tests cover nonce/form
requests, origin-local cookies, history, own-cookie authentication, model
restrictions, UTF-8 boundaries and truncation. No live account verification
has been performed for these sources.
