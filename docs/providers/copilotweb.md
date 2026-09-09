# Microsoft Copilot web

Native Go implementation of the [pinned Copilot web protocol](https://github.com/OmniRoute/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/executors/copilot-web.ts). Supply your own access token in `KeyEnv`. The adapter creates a fresh `/c/api/start` conversation and opens the product `/c/api/chat?api-version=2` WebSocket. The token stays in that account's request; caller Authorization is never forwarded.

Supported selectors are `copilot` / `copilot-chat` (chat), `copilot-think` (reasoning), and `copilot-smart` (smart). These select product modes, not certified underlying model versions. Historical GPT/o-series aliases from the reference are not accepted.

One user text message is supported. Responses are buffered so `replaceText` can replace previous content correctly. Hashcash challenges permit at most two rounds, cap difficulty and iteration count, and honor cancellation. Cloudflare and other challenge types fail explicitly. Empty completion, premature socket close, upstream errors and generated media fail. No anonymous session rotation or quota reset is performed. The WebSocket currently dials directly; environment HTTP proxies apply only to the start request.

Local HTTP/WebSocket tests verify own-token routing, start/body contract, hashcash response and resend, authoritative text replacement, truncation/errors, challenge bounds and cancellation. No live account validation has been performed.
