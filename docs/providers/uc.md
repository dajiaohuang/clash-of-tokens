# UC persona web

Native Go implementation of the [pinned UC product protocol](https://github.com/OmniRoute/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/executors/uc.ts) and its `uc/constants.ts`, `clerkAuth.ts`, `protocol.ts`, `stream.ts` helpers. This is the consumer persona WebSocket path. `KeyEnv` requires `{"cookie":"__client=your-client-cookie","sid":"your-clerk-session-id","uid":"your-user-id"}`. The adapter mints a short-lived session JWT for each turn; email login, persistent cookie rotation and token caching are not implemented.

The upgrade carries only Origin and the minted token query parameter, with no caller headers or durable cookie. The persona frame carries a single user text and explicit product model shortname; no identity preamble is added. History, OpenAI tools, media, generation controls and session continuation are rejected. Model names are sent unchanged and are not certified as underlying model identities.

`raw_text` in the terminal text frame is authoritative and can revise earlier deltas. Both response modes therefore buffer the turn before exposing its final text; SSE responses report `X-COT-Delivery: buffered`. EOF without `end_of_stream`, quota/error frames and empty final answers fail. WebSocket messages and aggregate input/output are bounded; timeout/cancellation closes the connection. The WebSocket connection currently dials directly (unlike the HTTP token mint, it does not use environment HTTP proxy settings).

Local HTTP/WebSocket contract tests cover minting, credential separation, persona payload, authoritative replacement, errors and truncation. No live account test has been performed.
