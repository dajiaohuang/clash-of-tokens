# WordPress chat adapters

`chataigpt` (chataigpt.net) and `chatgptfree` (chatgptfree.ai) use native Go HTTP
requests. They accept one user text message, including text-only content parts.
History, system messages, tools, images and unsupported generation parameters
return 422. They do not execute browser challenges.

Set `Source.Project` to the website's `post_id`. Set the model's `upstream` to
the website's `bot_id`; keep its public `id` as a meaningful local model alias.
These identifiers must come from the current website configuration.

`Source.KeyEnv` names an environment variable containing JSON:

```json
{"nonce":"YOUR_CURRENT_AJAX_NONCE","cookie":"YOUR_COOKIE_HEADER","session_id":"YOUR_SESSION_ID"}
```

Nonce is required for both. Cookie and session_id are required for chatgptfree.
For chataigpt, a missing session_id generates a request-scoped UUID. Every call
uses a fresh conversation UUID. The adapters do not share or discover existing
conversations, rotate accounts, or refresh expired credentials.

The first POST submits `aipkit_cache_sse_message`. A subsequent GET consumes
`aipkit_frontend_chat_stream` with its returned cache key. JSON `delta` fields
become OpenAI deltas; `finished`, `event: done`, or `[DONE]` must terminate the
stream. Unexpected EOF and upstream error events fail, and upstream error text
is not copied into replies. Whitespace is preserved. Non-streaming requests
aggregate the same stream within the package's 16 MiB bound. Token counts are
omitted.

Both adapters passed local request/authentication and stream conversion tests.
Neither has been validated against a live account; a configured adapter may
still encounter expired nonces or a browser verification requirement.

Protocol references (Apache-2.0; license retained in `docs/licenses/lza6-Apache-2.0.txt`):

- [lza6/Chataigpt-2api](https://github.com/lza6/Chataigpt-2api/tree/2eb9f0aad24b6547ebf098b0e32d5460849277f3), `app/providers/chataigpt_provider.py`.
- [lza6/FreeAIchat-2api](https://github.com/lza6/FreeAIchat-2api/tree/a1808e62f37392049a9b7cd10dba81589abaaf6a), `app/providers/freeaichat_provider.py`.
