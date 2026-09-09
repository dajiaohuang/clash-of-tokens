# Poe web

The Go adapter follows [poe-api-wrapper api.py](https://github.com/snowby666/poe-api-wrapper/blob/bbb6831eb1ccbc2bed7bae89cfbd29a106bdd707/poe_api_wrapper/api.py)
and its pinned `queries.py` / `utils.py`, including GraphQL persisted hashes,
the payload/formkey signature, subscription setup and tchannel WebSocket.
It does not use the unrelated simplified `chatWithBot` query in OmniRoute.

Provide the source environment credential as
`{"cookie":"p-b=YOUR_VALUE; p-lat=YOUR_VALUE","formkey":"YOUR_FORMKEY"}`.
Only explicitly supplied credentials are used; formkey extraction, refresh,
login and account creation are not implemented. No third-party proxy runs.

Use an exact product bot handle or a pinned internal model ID. The pinned
frontend/internal name map includes `GPT-4o` / `gpt4_o` and
`Assistant` / `capybara`. Stale names and persisted query hashes may require
upstream protocol updates; no real account test has been performed.

One user text message creates a fresh chat. The adapter subscribes before
submitting, buffers bounded channel frames, correlates the returned chat ID,
ignores other chats and human echoes, and returns only the authoritative
`complete` text. Sessions, history, tools and attachments are rejected.
Streaming output is buffered and marked `X-COT-Delivery: buffered`.
Transport truncation, cancellation, channel resets and application errors fail.
No retry re-sends a submitted message.

The source's bot-info projection omits display price, so this port follows its
null `messagePointsDisplayPrice`; it does not guess a token price. WSS with
certificate validation replaces the reference's insecure WS/disabled TLS checks.
Channel hosts are restricted to the pinned Poe host family. Source HTTP errors
are redacted and redirects are disabled.

Local HTTP/WebSocket fixtures validate operation hashes and signature, cookies,
bot mapping, subscribe/send ordering, chat correlation, authoritative completion,
open-connection terminal handling and truncated/error responses.
