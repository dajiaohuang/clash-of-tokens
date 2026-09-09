# Cloudflare AI Playground

`cloudflare-playground` is a native Go adapter that uses the configured
Chrome CDP connection. Each call opens a new tab at
`https://playground.ai.cloudflare.com/`, opens the product's WebSocket inside
that page, and closes the tab when the response finishes or is cancelled.
It does not use the Workers AI developer API.

Set `browser.enabled` and run the configured Chrome first. The public
playground uses no configured account token. Browser challenges and blocked
WebSocket upgrades are errors; the adapter does not solve them. Concurrency
is limited to one request per configured source.

The adapter supports Chat Completions with user/assistant text history and
temperature. System instructions, tools, images, unsupported fields and
`X-COT-Session` are rejected. A request owns a fresh room; supply complete text
history on each call. Model IDs receive the `@cf/` prefix when omitted.

The protocol sends `cf_agent_stream_resume_request`, the `setConfig` RPC,
then `cf_agent_use_chat_request`. Only responses with the exact chat ID can
complete a turn. A finish event and matching `done` are both required;
RPC acknowledgements do not count. Text and reasoning deltas are returned
incrementally, with no estimated token usage.

Browser queues, frames, total input/output and wall time are bounded. Slow
consumers that overflow the browser queue receive an error rather than losing
text. Closing the response cancels the browser transport. The contract tests
exercise correlation, terminal markers, unsupported inputs and cancellation.
A real public-upstream short-text request passed in 2.928 seconds on
2026-09-09; see [the validation record](../LIVE_VALIDATION.md).

Protocol reference:
[OmniRoute ba597b631d22d85e56db6982f24b7d1ebe238df9](https://github.com/diegosouzapw/OmniRoute/tree/ba597b631d22d85e56db6982f24b7d1ebe238df9),
`open-sse/executors/cloudflare-playground.ts` (MIT). The Go implementation and
browser bridge were independently written from the protocol facts; it does
not launch the reference executor or depend on its Playwright service.
