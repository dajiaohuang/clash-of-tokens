# EaseMate browser

Native Go Chrome DevTools implementation based on the pinned `lza6/easemate-2api` provider referenced in the catalog. Enable the configured browser and connect to an existing Chrome; each request opens and closes its own tab in that browser account. Login is handled by the user in Chrome. No cookie or token is extracted into the gateway.

Use the exact model label displayed in the site's picker, such as `GPT-4o mini`, rather than the reference's historical aliases that map one model version to another. The adapter confirms the selected label, fills one user message, invokes the site's send button and observes the site's own fetch or XHR request to `api.easemate.ai/api2/stream/exec_operation`. Unsupported history/tools/controls/session continuation fail. Missing models and failed sends do not silently use the currently selected model.

Both response modes buffer the captured response through successful HTTP EOF. Nested SSE `code:200` and JSON `data.answer` become output; malformed, empty or error records fail. Browser capture, response conversion and turn duration are bounded. SSE responses carry `X-COT-Delivery: buffered`. UI selectors can change and production account availability has not been tested.

Tests include envelope/error parsing and a real Chrome local fixture covering exact picker selection, textarea events, send, fetch and XHR response capture. Run that fixture with `COT_TEST_CDP=http://127.0.0.1:9222`; it accesses only a local test server. This is browser plumbing validation, not live EaseMate verification.
