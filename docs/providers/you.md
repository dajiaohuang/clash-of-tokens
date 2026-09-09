# You.com

Native Go migration of [g4f You.py at the pinned revision](https://github.com/xtekky/gpt4free/blob/502b0e2e8d82292a39d2c3d9da07125322870583/g4f/Provider/needs_auth/You.py).
The reference explicitly sets `working = False`. This adapter has local protocol
tests, not a claim of current production availability.

Set the source environment credential to your own You.com Cookie header or JSON
containing `cookie`. No credentials are extracted automatically. Send one user
text message with the exact product model ID. The pinned default `gpt-4o-mini`
uses default mode; other text IDs use custom mode with hyphens replaced by
underscores. Agent/image modes, history, tools and sessions are rejected.

The adapter calls `/api/streamingSearch` and decodes `youChatToken` and
`youChatUpdate` events. It buffers the answer and accepts clean HTTP EOF, as
defined by the reference, but rejects transport truncation, error events, quota
notices, malformed text and empty output. No token usage is invented.
Fixtures verify query escaping, cookie forwarding, event decoding and failures.
