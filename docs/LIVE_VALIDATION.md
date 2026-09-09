# Live validation record — 2026-09-09

ChatGPT Web has been tested with a real signed-in account. Cloudflare's public
AI Playground has also completed a real upstream request through Chrome,
without an account token. Other provider evidence consists of local protocol
fixtures and source review. Registration counts in `SOURCE_STATUS.md` are not
live availability counts.

The user authorized a dedicated Chrome login. The Go gateway connected through
the local CDP endpoint and submitted prompts through the webpage. The gateway
listened at `127.0.0.1:18317`; the default port 8317 was unavailable on this
Windows host. No browser cookies or access tokens were exported into gateway
configuration.

| Test | Result | Elapsed |
| --- | --- | ---: |
| Non-streaming Responses, first turn | Exact requested codeword | 10.109 s |
| Non-streaming follow-up using previous_response_id | Same codeword recalled | 8.155 s |
| Streaming Responses, first turn | Text deltas and response.completed agree | 10.094 s |
| Streaming follow-up | Same codeword recalled | 9.461 s |
| Streaming follow-up after stopping, rebuilding and restarting gateway | Persisted response mapping resumed the previous webpage conversation; same codeword recalled | 9.608 s |

The webpage-reported model was `gpt-5-6-thinking`. This records the observed
model label; it does not independently certify model weights or entitlement.
No token usage was available, so the gateway returned `usage: null`.

Reproduction uses `scripts/smoke_chatgpt.py --live` or `--live --stream`.
The script asserts the exact answer, completed status and (for streaming)
matching deltas plus a completion event. After a restart,
`--previous-id YOUR_PRIOR_SMOKE_RESPONSE_ID` performs just the recall turn.
These commands submit real prompts and require the existing authorized login.

The tests establish short text conversation, streaming and restart continuity
for this account at this time. They do not validate tools, images, general
model selection or parallel browser requests.

## Cloudflare Playground

On 2026-09-09 (Asia/Singapore), the native Go browser WebSocket adapter sent
one short prompt to `@cf/zai-org/glm-4.7-flash` at the public playground. A
non-streaming Chat Completions result contained the requested phrase
`copper-cloud-29` in 2.928 seconds. The adapter observed the matching finish
and done frames. This was a direct provider test using the same configured
Chrome CDP connection, not an official Workers AI API call or a gateway load
test. It does not certify model identity or general availability.

Reproduction is opt-in: set `COT_PLAYGROUND_LIVE=1` in the test process and run
`go test ./internal/providers/playground -run TestLivePlayground -count=1 -v`.
This submits a real request. Normal tests skip it.

## Final strict93 build check

After registering all 93 source paths and rebuilding the gateway, the real ChatGPT streaming smoke test passed again: first turn 16.158 seconds, follow-up 10.275 seconds. Both returned the exact codeword, matching SSE deltas and response.completed. The rebuilt gateway is listening at 127.0.0.1:18317.

Windows full-package tests and go vet passed. Linux race checks passed for businessweb, codingfinal, codingmore, majorweb, upstream and catalog. Local Chrome fixtures passed for AI Studio Build, GigaChat, Gemini, DuckDuckGo and TinyCMS; these fixtures do not count as live account verification. Qoder timeout validation uses a sleeping helper rather than an empty select, which Go may terminate as a deadlock on Linux.
