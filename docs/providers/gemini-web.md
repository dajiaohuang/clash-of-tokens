# Gemini Web

This adapter uses native Go Chrome DevTools Protocol (CDP) automation against the configured `gemini.google.com` page. Chrome must already be running with the user's Google account signed in; the gateway does not extract or persist cookies, tokens, or browser fingerprints.

Each request opens a fresh page, accepts one user text message, dispatches one Enter keydown event, and captures the page's own `StreamGenerate` response. The response format contains cumulative `wrb.fr` snapshots, so the adapter returns the final snapshot (an empty final snapshot is an error). OpenAI-compatible streaming is pseudo-streaming: the complete answer is emitted as one content chunk followed by the terminal chunk.

The adapter supports `chat` with the model selector `web` and text-only input. The model selector names the browser account, while Gemini Web's page controls choose the account's currently available web model. Other requested model labels, history, tools, attachments, and `X-COT-Session` continuation are rejected rather than silently discarded.

Configure `browser.enabled=true` and a loopback `browser.cdp_url`. The catalog entry is based on the pinned OmniRoute commit `ba597b631d22d85e56db6982f24b7d1ebe238df9`, specifically `open-sse/executors/gemini-web.ts`, and its `StreamGenerate` parser. Local CDP fixture tests cover navigation, page input, capture, cumulative-frame parsing, and buffered/stream response conversion. `live_verified` remains false until a real signed-in Gemini account is exercised.

Submission uses one page event and is not retried if the page does not react; this avoids duplicate turns when a request starts asynchronously. Sites requiring trusted keyboard input may reject this path. Browser fixture success does not establish compatibility with the current signed-in site.
