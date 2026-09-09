# Duck.ai

`duck-ai` selects the `duckduckgo-web` browser adapter. Enable Chrome/CDP and
use model `web`. One user text message is supported; the page's current model
selection is used, and the model captured from the actual outgoing request is
returned. This adapter does not pretend to select an arbitrary requested model.

The adapter opens a fresh `/chat` tab, uses the page textarea and Ask button,
and captures its own `POST /duckchat/v1/chat` fetch. It limits captured response
bytes and requires valid SSE ending in `[DONE]`. Responses are buffered. The
page retains control of authentication, anonymous-session handling and request
headers; the gateway does not generate guest credentials.

The browser flow is based on the
[pinned OmniRoute executor](https://github.com/diegosouzapw/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/executors/duckduckgo-web.ts).
An actual Chrome test against a local fixture passed on 2026-09-09, verifying
the submitted prompt, captured response and returned model identity. Real
Duck.ai access, current selectors and any first-visit consent flow remain
unverified. The capture currently supports fetch, not XMLHttpRequest.
